-- Ontario output by fuel per UTC hour, from IESO's Generator Output by Fuel Type
-- Hourly report (complete history; IESO's own aggregate of all generators).
-- output_quality is IESO's flag for data points not available, kept as published.
DROP VIEW IF EXISTS silver.hourly_generation CASCADE;
CREATE VIEW silver.hourly_generation AS
SELECT 'ON'::text                        AS province,
       interval_start_utc                AS hour_utc,
       COALESCE(fuel_type, 'UNKNOWN')    AS fuel_type,
       output_mw                         AS generation_mw,
       output_quality,
       output_mw IS NOT NULL             AS is_complete
FROM staging.ieso_fuel_hourly;
