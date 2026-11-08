-- +goose Up

-- Loads are skipped only when the bronze bytes are unchanged, so a file that
-- was overwritten upstream (same key, new revision) is picked up again.
ALTER TABLE staging.load_log ADD COLUMN content_sha256 text NOT NULL DEFAULT '';

CREATE TABLE staging.ieso_generator_hourly (
    interval_start_utc timestamptz      NOT NULL,
    generator          text             NOT NULL,
    fuel_type          text,
    hour_ending        smallint         NOT NULL,
    energy_mw          double precision, -- NULL when IESO published no value
    bronze_key         text             NOT NULL,
    loaded_at          timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (interval_start_utc, generator)
);

CREATE TABLE staging.ieso_demand_hourly (
    interval_start_utc timestamptz PRIMARY KEY,
    hour_ending        smallint         NOT NULL,
    market_demand_mw   double precision,
    ontario_demand_mw  double precision,
    snapshot_utc       timestamptz      NOT NULL, -- the file's "Created at"; newest wins
    bronze_key         text             NOT NULL,
    loaded_at          timestamptz      NOT NULL DEFAULT now()
);

-- Day-ahead (forecast-style) zonal price in $/MWh, not the settled real-time price.
CREATE TABLE staging.ieso_da_price_hourly (
    interval_start_utc timestamptz PRIMARY KEY,
    hour_ending        smallint         NOT NULL,
    zonal_price        double precision,
    loss_price         double precision,
    congestion_price   double precision,
    flag               text,
    bronze_key         text             NOT NULL,
    loaded_at          timestamptz      NOT NULL DEFAULT now()
);

CREATE TABLE staging.aeso_pool_price_hourly (
    interval_start_utc  timestamptz PRIMARY KEY,
    pool_price          double precision, -- NULL until the hour has settled
    forecast_pool_price double precision,
    rolling_30day_avg   double precision,
    bronze_key          text        NOT NULL,
    loaded_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE staging.aeso_load_hourly (
    interval_start_utc timestamptz PRIMARY KEY,
    load_mw            double precision, -- NULL for hours that have not happened yet
    forecast_load_mw   double precision,
    bronze_key         text        NOT NULL,
    loaded_at          timestamptz NOT NULL DEFAULT now()
);

-- Measurements are in the units ECCC publishes; flags are ECCC quality flags.
CREATE TABLE staging.eccc_weather_hourly (
    climate_id             text             NOT NULL,
    time_utc               timestamptz      NOT NULL,
    station_name           text,
    province               text,
    temp                   double precision,
    temp_flag              text,
    dew_point              double precision,
    relative_humidity      double precision,
    relative_humidity_flag text,
    wind_speed             double precision,
    wind_speed_flag        text,
    wind_direction         double precision,
    station_pressure       double precision,
    visibility             double precision,
    precip_amount          double precision,
    bronze_key             text             NOT NULL,
    loaded_at              timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (climate_id, time_utc)
);

-- +goose Down
DROP TABLE staging.eccc_weather_hourly;
DROP TABLE staging.aeso_load_hourly;
DROP TABLE staging.aeso_pool_price_hourly;
DROP TABLE staging.ieso_da_price_hourly;
DROP TABLE staging.ieso_demand_hourly;
DROP TABLE staging.ieso_generator_hourly;
ALTER TABLE staging.load_log DROP COLUMN content_sha256;
