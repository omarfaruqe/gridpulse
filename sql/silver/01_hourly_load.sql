-- Electricity demand at one grain: province x UTC hour, in MW.
-- Ontario uses "Ontario demand" (not market demand, which includes exports).
-- Hydro-Quebec publishes 15-minute slots; an hour gets a value only when all
-- four slots are present, so a partly published hour is NULL, never a guess.
DROP VIEW IF EXISTS silver.hourly_load CASCADE;
CREATE VIEW silver.hourly_load AS
WITH hq AS (
    SELECT to_timestamp(floor(extract(epoch FROM time_utc)::double precision / 3600) * 3600) AS hour_utc,
           avg(demand_mw)   AS avg_mw,
           count(demand_mw) AS slots_present
    FROM staging.hq_demand_15min
    GROUP BY 1
)
SELECT 'QC'::text AS province,
       hour_utc,
       CASE WHEN slots_present = 4 THEN avg_mw END AS load_mw,
       slots_present = 4                           AS is_complete,
       'hydroquebec_demand'::text                  AS source
FROM hq
UNION ALL
SELECT 'ON', interval_start_utc, ontario_demand_mw, ontario_demand_mw IS NOT NULL, 'ieso_demand'
FROM staging.ieso_demand_hourly
UNION ALL
SELECT 'AB', interval_start_utc, load_mw, load_mw IS NOT NULL, 'aeso_alberta_internal_load'
FROM staging.aeso_load_hourly;
