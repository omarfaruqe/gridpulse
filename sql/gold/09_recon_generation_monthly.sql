-- Ontario monthly generation: IESO's hourly report summed per month, against
-- Statistics Canada table 25-10-0015-01 (class "Total all classes of producer").
--
-- The two are NOT expected to be equal. IESO's report sums the output it
-- measures per fuel; StatCan counts every producer. So each month's ratio
-- (IESO / StatCan) is compared with that group's own recent history instead:
-- the baseline is the median ratio of the previous 12 comparable months, and a
-- month is flagged when it moves further from the baseline than the group's
-- tolerance in ops.recon_tolerance.
--
-- Months are IESO delivery months (fixed EST, as IESO labels its days).
-- StatCan's treatment of month boundaries is not documented; the possible
-- one-hour shift at a boundary is well under 0.2% of a month.
--
-- Completeness is judged on the six named generation fuels (hydro, nuclear, wind,
-- solar, gas, biofuel): IESO's OTHER (from 2025-04) and CONTROL ACTIONS (a few
-- hours in 2026-07) are summed into the Total's energy but are too small and
-- sporadic to be required.
--
-- Status: ok | out_of_band | incomplete (under 99.5% of the group's hourly
-- points present) | no_baseline (fewer than 6 comparable months before) |
-- not_comparable (StatCan value is 0) | no_tolerance (group missing from ops.recon_tolerance).
DROP TABLE IF EXISTS gold.recon_generation_monthly CASCADE;
CREATE TABLE gold.recon_generation_monthly AS
WITH core_fuels(comparison_group, n_fuels) AS (
    VALUES ('Hydro', 1), ('Nuclear', 1), ('Wind', 1), ('Solar', 1), ('Combustible', 2), ('Total', 6)
),
ieso_fuel AS (
    SELECT date_trunc('month', (hour_utc AT TIME ZONE 'UTC') - interval '5 hours')::date AS ref_month,
           CASE fuel_type
               WHEN 'HYDRO'   THEN 'Hydro'
               WHEN 'NUCLEAR' THEN 'Nuclear'
               WHEN 'WIND'    THEN 'Wind'
               WHEN 'SOLAR'   THEN 'Solar'
               WHEN 'GAS'     THEN 'Combustible'
               WHEN 'BIOFUEL' THEN 'Combustible'
           END AS grp,
           count(generation_mw) AS points_with_value,
           sum(generation_mw)   AS mwh
    FROM gold.fact_generation_by_fuel
    WHERE province_code = 'ON'
    GROUP BY 1, fuel_type, 2
),
ieso AS (
    SELECT ref_month, grp AS comparison_group,
           sum(mwh) AS ieso_mwh, sum(points_with_value) AS points_with_value
    FROM ieso_fuel WHERE grp IS NOT NULL
    GROUP BY ref_month, grp
    UNION ALL
    SELECT ref_month, 'Total',
           sum(mwh),                                              -- every fuel's energy
           sum(points_with_value) FILTER (WHERE grp IS NOT NULL)  -- completeness: core fuels only
    FROM ieso_fuel
    GROUP BY ref_month
),
statcan AS (
    SELECT ref_month,
           CASE generation_type
               WHEN 'Hydraulic turbine'                                  THEN 'Hydro'
               WHEN 'Nuclear steam turbine'                              THEN 'Nuclear'
               WHEN 'Wind power turbine'                                 THEN 'Wind'
               WHEN 'Solar'                                              THEN 'Solar'
               WHEN 'Total electricity production from combustible fuels' THEN 'Combustible'
               WHEN 'Total all types of electricity generation'          THEN 'Total'
           END AS comparison_group,
           mwh AS statcan_mwh
    FROM staging.statcan_generation_monthly
    WHERE geo = 'Ontario'
      AND producer_class = 'Total all classes of electricity producer'
      AND mwh IS NOT NULL
),
base AS (
    SELECT s.ref_month, s.comparison_group, i.ieso_mwh, s.statcan_mwh,
           i.ieso_mwh / NULLIF(s.statcan_mwh, 0) AS ratio,
           i.points_with_value::numeric
             / (c.n_fuels * ((s.ref_month + interval '1 month')::date - s.ref_month) * 24) AS ieso_coverage
    FROM statcan s
    JOIN ieso i ON i.ref_month = s.ref_month AND i.comparison_group = s.comparison_group
    JOIN core_fuels c ON c.comparison_group = s.comparison_group
),
scored AS (
    SELECT b.*,
           (SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY p.ratio)
              FROM base p
             WHERE p.comparison_group = b.comparison_group
               AND p.ref_month < b.ref_month
               AND p.ref_month >= b.ref_month - interval '12 months'
               AND p.ieso_coverage >= 0.995 AND p.ratio IS NOT NULL) AS baseline_ratio,
           (SELECT count(*)
              FROM base p
             WHERE p.comparison_group = b.comparison_group
               AND p.ref_month < b.ref_month
               AND p.ref_month >= b.ref_month - interval '12 months'
               AND p.ieso_coverage >= 0.995 AND p.ratio IS NOT NULL) AS baseline_months
    FROM base b
)
SELECT s.ref_month, s.comparison_group,
       s.ieso_mwh, s.statcan_mwh, s.ratio, s.ieso_coverage,
       s.baseline_ratio, s.baseline_months,
       s.ratio - s.baseline_ratio AS deviation,
       t.max_deviation            AS tolerance,
       CASE
           WHEN s.ratio IS NULL                       THEN 'not_comparable'
           WHEN s.ieso_coverage < 0.995               THEN 'incomplete'
           WHEN t.max_deviation IS NULL               THEN 'no_tolerance'
           WHEN s.baseline_months < 6                 THEN 'no_baseline'
           WHEN abs(s.ratio - s.baseline_ratio) > t.max_deviation THEN 'out_of_band'
           ELSE 'ok'
       END AS status
FROM scored s
LEFT JOIN ops.recon_tolerance t USING (comparison_group);
ALTER TABLE gold.recon_generation_monthly ADD PRIMARY KEY (ref_month, comparison_group);
