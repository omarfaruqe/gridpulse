-- Hours inside the range of the real-time Ontario price that have no row. A
-- gap means an hour file was never fetched (for example the scheduler was down
-- for longer than its catch-up window); backfill it with the extractor.
SELECT h AS missing_hour
FROM (SELECT min(hour_utc) AS lo, max(hour_utc) AS hi
      FROM gold.fact_hourly_price WHERE price_kind = 'real_time_zonal_avg') s
CROSS JOIN LATERAL generate_series(s.lo, s.hi, interval '1 hour') AS h
WHERE NOT EXISTS (
    SELECT 1 FROM gold.fact_hourly_price f
    WHERE f.price_kind = 'real_time_zonal_avg' AND f.hour_utc = h)
