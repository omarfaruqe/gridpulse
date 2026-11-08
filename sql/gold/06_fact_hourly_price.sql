DROP TABLE IF EXISTS gold.fact_hourly_price CASCADE;
CREATE TABLE gold.fact_hourly_price AS
SELECT province AS province_code, hour_utc, price_cad_mwh, price_kind, source
FROM silver.hourly_price;
ALTER TABLE gold.fact_hourly_price ADD PRIMARY KEY (province_code, hour_utc);
