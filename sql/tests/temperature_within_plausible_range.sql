SELECT province_code, hour_utc, temp_c FROM gold.fact_hourly_weather
WHERE temp_c IS NOT NULL AND temp_c NOT BETWEEN -60 AND 50
