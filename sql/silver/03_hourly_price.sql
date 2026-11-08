-- Prices in CAD/MWh. The two series are NOT the same thing and price_kind says
-- which is which: Ontario is the day-ahead zonal price, Alberta is the settled
-- pool price. Do not compare them as like for like.
DROP VIEW IF EXISTS silver.hourly_price CASCADE;
CREATE VIEW silver.hourly_price AS
SELECT 'ON'::text AS province, interval_start_utc AS hour_utc,
       zonal_price AS price_cad_mwh, 'day_ahead_zonal'::text AS price_kind,
       'ieso_dahourlyontariozonalprice'::text AS source
FROM staging.ieso_da_price_hourly
UNION ALL
SELECT 'AB', interval_start_utc, pool_price, 'pool_price', 'aeso_poolprice'
FROM staging.aeso_pool_price_hourly;
