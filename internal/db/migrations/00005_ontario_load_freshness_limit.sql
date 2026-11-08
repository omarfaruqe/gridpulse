-- +goose Up
-- Observed 2026-11-08: IESO regenerates PUB_Demand_<year>.csv once a day at
-- about 07:30 Eastern ("Created at" 07:30:11 on 11-08, 07:30:15 on 11-07), with
-- data up to hour 1 of that day. The newest hour is therefore up to ~32 hours
-- old just before the next release, so the original 6-hour limit was wrong.
UPDATE ops.freshness_sla
SET max_lag = interval '36 hours',
    note    = 'IESO Demand yearly file is regenerated once a day (~07:30 ET), so up to ~32h old before the next release'
WHERE series = 'load_ON';

-- +goose Down
UPDATE ops.freshness_sla
SET max_lag = interval '6 hours',
    note    = 'IESO Demand yearly file, refreshed through the day'
WHERE series = 'load_ON';
