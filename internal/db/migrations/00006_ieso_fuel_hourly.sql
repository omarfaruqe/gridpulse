-- +goose Up
-- IESO "Generator Output by Fuel Type Hourly" report: total output per fuel per
-- hour, published as one file per year back to at least 2022.
CREATE TABLE staging.ieso_fuel_hourly (
    interval_start_utc timestamptz      NOT NULL,
    fuel_type          text             NOT NULL,
    hour_ending        smallint         NOT NULL,
    output_mw          double precision, -- NULL when IESO published no Output
    output_quality     smallint,         -- IESO's "number of data points not available" flag; stored as published
    snapshot_utc       timestamptz      NOT NULL, -- the file's CreatedAt; newest wins
    bronze_key         text             NOT NULL,
    loaded_at          timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (interval_start_utc, fuel_type)
);

-- +goose Down
DROP TABLE staging.ieso_fuel_hourly;
