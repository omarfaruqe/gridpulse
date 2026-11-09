-- +goose Up
-- IESO real-time 5-minute Ontario zonal energy price (RealtimeOntarioZonalPrice),
-- one row per published five-minute interval. Intervals not yet published are
-- never stored. The price, loss and congestion columns come from the report's
-- LmpCap, LossPriceCap and CongPriceCap fields, as published.
CREATE TABLE staging.ieso_rt_price_5min (
    interval_start_utc timestamptz      PRIMARY KEY,
    delivery_hour      smallint         NOT NULL, -- hour ending, 1..24, EST
    interval_no        smallint         NOT NULL, -- 1..12 within the hour
    zonal_price        double precision NOT NULL,
    loss_price         double precision,
    congestion_price   double precision,
    flag               text,                      -- stored as published; meaning not documented here
    snapshot_utc       timestamptz      NOT NULL, -- the file's CreatedAt; newest wins
    bronze_key         text             NOT NULL,
    loaded_at          timestamptz      NOT NULL DEFAULT now()
);

-- The hourly real-time average appears once all 12 intervals are published, so
-- the newest complete hour is 1 to 2 hours old; 3 hours leaves room for a late
-- file. A first estimate: ops.freshness_lag will show what the series really does.
INSERT INTO ops.freshness_sla (series, max_lag, note) VALUES
    ('price_rt_ON', interval '3 hours', 'IESO real-time 5-minute zonal price, hourly average of complete hours');

-- +goose Down
DELETE FROM ops.freshness_sla WHERE series = 'price_rt_ON';
DROP TABLE staging.ieso_rt_price_5min;
