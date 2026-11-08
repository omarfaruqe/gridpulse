-- Ontario output by fuel per UTC hour. When some generators did not report,
-- generation_mw is the sum of those that did and is_complete is false, so a
-- consumer can tell an understated total from a full one.
DROP VIEW IF EXISTS silver.hourly_generation CASCADE;
CREATE VIEW silver.hourly_generation AS
SELECT 'ON'::text                              AS province,
       interval_start_utc                      AS hour_utc,
       COALESCE(fuel_type, 'UNKNOWN')          AS fuel_type,
       sum(energy_mw)                          AS generation_mw,
       count(energy_mw)                        AS generators_reporting,
       count(*)                                AS generators_total,
       count(energy_mw) = count(*)             AS is_complete
FROM staging.ieso_generator_hourly
GROUP BY interval_start_utc, COALESCE(fuel_type, 'UNKNOWN');
