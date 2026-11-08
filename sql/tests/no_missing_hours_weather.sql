-- Hours inside each province's range that have no row at all.
SELECT s.province_code, h AS missing_hour
FROM (SELECT province_code, min(hour_utc) AS lo, max(hour_utc) AS hi
      FROM gold.fact_hourly_weather GROUP BY province_code) s
CROSS JOIN LATERAL generate_series(s.lo, s.hi, interval '1 hour') AS h
WHERE NOT EXISTS (
    SELECT 1 FROM gold.fact_hourly_weather f WHERE f.province_code = s.province_code AND f.hour_utc = h)
