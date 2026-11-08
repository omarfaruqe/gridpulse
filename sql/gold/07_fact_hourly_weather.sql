DROP TABLE IF EXISTS gold.fact_hourly_weather CASCADE;
CREATE TABLE gold.fact_hourly_weather AS
SELECT province AS province_code, hour_utc, temp_c, wind_speed_kmh,
       stations_reporting, stations_total, is_complete
FROM silver.hourly_weather;
ALTER TABLE gold.fact_hourly_weather ADD PRIMARY KEY (province_code, hour_utc);
