-- +goose Up
CREATE SCHEMA IF NOT EXISTS staging;

-- One row per bronze file that has been loaded: lineage and idempotency.
CREATE TABLE staging.load_log (
    id         bigserial PRIMARY KEY,
    source     text        NOT NULL,
    bronze_key text        NOT NULL,
    row_count  integer     NOT NULL,
    loaded_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, bronze_key)
);

-- Hydro-Quebec real-time demand, 15-minute slots. The latest snapshot wins.
CREATE TABLE staging.hq_demand_15min (
    time_utc        timestamptz      PRIMARY KEY,
    local_time      text             NOT NULL, -- raw Eastern timestamp from the feed
    demand_mw       double precision,          -- NULL when the slot has no value yet
    time_resolution text             NOT NULL CHECK (time_resolution IN ('unique', 'ambiguous', 'nonexistent')),
    snapshot_utc    timestamptz      NOT NULL,
    bronze_key      text             NOT NULL,
    loaded_at       timestamptz      NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE staging.hq_demand_15min;
DROP TABLE staging.load_log;
DROP SCHEMA staging;
