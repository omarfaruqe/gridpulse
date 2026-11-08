SELECT province_code, hour_utc, price_cad_mwh FROM gold.fact_hourly_price
WHERE price_cad_mwh IS NOT NULL AND price_cad_mwh NOT BETWEEN -500 AND 5000
