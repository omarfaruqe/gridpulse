SELECT 'load' AS model, province_code, hour_utc FROM gold.fact_hourly_load
WHERE is_complete AND load_mw IS NULL
UNION ALL
SELECT 'weather', province_code, hour_utc FROM gold.fact_hourly_weather
WHERE is_complete AND temp_c IS NULL
