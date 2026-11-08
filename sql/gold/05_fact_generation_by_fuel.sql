DROP TABLE IF EXISTS gold.fact_generation_by_fuel CASCADE;
CREATE TABLE gold.fact_generation_by_fuel AS
SELECT province AS province_code, hour_utc, fuel_type, generation_mw, output_quality, is_complete
FROM silver.hourly_generation;
ALTER TABLE gold.fact_generation_by_fuel ADD PRIMARY KEY (province_code, hour_utc, fuel_type);
