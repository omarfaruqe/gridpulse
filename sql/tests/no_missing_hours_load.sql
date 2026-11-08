-- Hours inside each province's range that have no row at all.
--
-- Known upstream gaps (confirmed against the published file, not a pipeline
-- fault). Add a line here only after checking the source:
--   ON 2025-05-01 05:00 UTC (hour ending 1 EST): IESO's PUB_Demand_2025.csv
--   jumps from 2025-04-30 hour 24 to 2025-05-01 hour 2.
SELECT s.province_code, h AS missing_hour
FROM (SELECT province_code, min(hour_utc) AS lo, max(hour_utc) AS hi
      FROM gold.fact_hourly_load GROUP BY province_code) s
CROSS JOIN LATERAL generate_series(s.lo, s.hi, interval '1 hour') AS h
WHERE NOT EXISTS (
    SELECT 1 FROM gold.fact_hourly_load f WHERE f.province_code = s.province_code AND f.hour_utc = h)
  AND (s.province_code, h) NOT IN (VALUES ('ON', '2025-05-01 05:00+00'::timestamptz))
