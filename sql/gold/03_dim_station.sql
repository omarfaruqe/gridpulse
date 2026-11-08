DROP TABLE IF EXISTS gold.dim_station CASCADE;
CREATE TABLE gold.dim_station AS
SELECT climate_id, name AS station_name, province AS station_province,
       region AS province_code, operator AS grid_operator, weight
FROM staging.station_region;
ALTER TABLE gold.dim_station ADD PRIMARY KEY (climate_id);
