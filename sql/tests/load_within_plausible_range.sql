SELECT province_code, hour_utc, load_mw FROM gold.fact_hourly_load
WHERE load_mw IS NOT NULL AND load_mw NOT BETWEEN 1000 AND 60000
