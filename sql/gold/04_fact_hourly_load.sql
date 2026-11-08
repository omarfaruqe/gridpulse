DROP TABLE IF EXISTS gold.fact_hourly_load CASCADE;
CREATE TABLE gold.fact_hourly_load AS
SELECT province AS province_code, hour_utc, load_mw, is_complete, source
FROM silver.hourly_load;
ALTER TABLE gold.fact_hourly_load ADD PRIMARY KEY (province_code, hour_utc);
