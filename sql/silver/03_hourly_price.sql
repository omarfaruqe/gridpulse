-- Prices in CAD/MWh. The series are NOT the same thing and price_kind says which
-- is which: Ontario has the day-ahead zonal price and, separately, the simple
-- mean of the 5-minute real-time zonal prices of the hour (only for hours with
-- all 12 intervals published; it is not an official hourly price). Alberta is
-- the settled pool price. Do not compare the kinds as like for like.
DROP VIEW IF EXISTS silver.hourly_price CASCADE;
CREATE VIEW silver.hourly_price AS
SELECT 'ON'::text AS province, interval_start_utc AS hour_utc,
       zonal_price AS price_cad_mwh, 'day_ahead_zonal'::text AS price_kind,
       'ieso_dahourlyontariozonalprice'::text AS source
FROM staging.ieso_da_price_hourly
UNION ALL
SELECT 'ON', date_trunc('hour', interval_start_utc AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
       avg(zonal_price), 'real_time_zonal_avg', 'ieso_realtimeontariozonalprice'
FROM staging.ieso_rt_price_5min
GROUP BY 2
HAVING count(*) = 12
UNION ALL
SELECT 'AB', interval_start_utc, pool_price, 'pool_price', 'aeso_poolprice'
FROM staging.aeso_pool_price_hourly;
