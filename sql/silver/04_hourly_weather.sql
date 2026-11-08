-- Region weather per UTC hour: a population-weighted mean over the region's
-- stations. A missing station reading is left out and the remaining weights
-- are renormalised; stations_reporting vs stations_total shows how many
-- readings the number rests on.
DROP VIEW IF EXISTS silver.hourly_weather CASCADE;
CREATE VIEW silver.hourly_weather AS
WITH expected AS (
    SELECT region, count(*) AS stations_total FROM staging.station_region GROUP BY region
)
SELECT s.region AS province,
       w.time_utc AS hour_utc,
       sum(s.weight * w.temp)       FILTER (WHERE w.temp IS NOT NULL)
         / NULLIF(sum(s.weight)     FILTER (WHERE w.temp IS NOT NULL), 0) AS temp_c,
       sum(s.weight * w.wind_speed) FILTER (WHERE w.wind_speed IS NOT NULL)
         / NULLIF(sum(s.weight)     FILTER (WHERE w.wind_speed IS NOT NULL), 0) AS wind_speed_kmh,
       count(w.temp)                AS stations_reporting,
       e.stations_total,
       count(w.temp) = e.stations_total AS is_complete
FROM staging.eccc_weather_hourly w
JOIN staging.station_region s USING (climate_id)
JOIN expected e ON e.region = s.region
GROUP BY s.region, w.time_utc, e.stations_total;
