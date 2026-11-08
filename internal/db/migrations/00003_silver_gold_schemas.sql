-- +goose Up
CREATE SCHEMA IF NOT EXISTS silver;
CREATE SCHEMA IF NOT EXISTS gold;

-- Station-to-region mapping, seeded from the embedded stations.json by the
-- transform step so the weights used in SQL are the ones reviewed in Git.
CREATE TABLE staging.station_region (
    climate_id text PRIMARY KEY,
    name       text             NOT NULL,
    province   text             NOT NULL,
    region     text             NOT NULL,
    operator   text             NOT NULL,
    weight     double precision NOT NULL CHECK (weight > 0 AND weight <= 1)
);

-- +goose Down
DROP TABLE staging.station_region;
DROP SCHEMA gold CASCADE;
DROP SCHEMA silver CASCADE;
