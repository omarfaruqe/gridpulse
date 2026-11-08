SELECT province_code, hour_utc, fuel_type, generation_mw
FROM gold.fact_generation_by_fuel WHERE generation_mw < 0
