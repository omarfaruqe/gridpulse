-- +goose Up
-- The lag of every series at every freshness check, so the limits in
-- ops.freshness_sla can be set from observed worst cases. Pruned to 90 days.
CREATE TABLE ops.freshness_lag (
    id              bigserial   PRIMARY KEY,
    checked_at      timestamptz NOT NULL,
    series          text        NOT NULL,
    latest_hour     timestamptz,           -- NULL when the series has no data
    lag_seconds     bigint,                -- NULL when the series has no data; negative when ahead of the clock
    max_lag_seconds bigint      NOT NULL,  -- the limit in force at the time
    stale           boolean     NOT NULL
);
CREATE INDEX freshness_lag_series_idx ON ops.freshness_lag (series, checked_at DESC);

-- +goose Down
DROP TABLE ops.freshness_lag;
