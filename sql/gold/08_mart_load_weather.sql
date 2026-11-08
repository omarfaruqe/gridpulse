-- Ready for analysis: hours where both load and temperature are known.
DROP TABLE IF EXISTS gold.mart_load_weather CASCADE;
CREATE TABLE gold.mart_load_weather AS
SELECT l.province_code, l.hour_utc, l.load_mw, w.temp_c, w.wind_speed_kmh,
       w.stations_reporting, w.stations_total
FROM gold.fact_hourly_load l
JOIN gold.fact_hourly_weather w USING (province_code, hour_utc)
WHERE l.load_mw IS NOT NULL AND w.temp_c IS NOT NULL;
ALTER TABLE gold.mart_load_weather ADD PRIMARY KEY (province_code, hour_utc);
