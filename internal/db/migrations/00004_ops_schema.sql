-- +goose Up
CREATE SCHEMA ops;

-- One row per scheduled job run: what ran, when, and how it ended.
CREATE TABLE ops.job_run (
    id          bigserial   PRIMARY KEY,
    job         text        NOT NULL,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status      text        NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    error       text
);
CREATE INDEX job_run_job_started_idx ON ops.job_run (job, started_at DESC);

-- How old the newest data point of a series may be before the series counts as
-- stale. These are INITIAL values chosen from how each feed publishes, not from
-- observation: tune them after the scheduler has run for a few days
-- (UPDATE ops.freshness_sla SET max_lag = ... WHERE series = ...).
CREATE TABLE ops.freshness_sla (
    series  text     PRIMARY KEY,
    max_lag interval NOT NULL CHECK (max_lag > interval '0'),
    note    text
);

INSERT INTO ops.freshness_sla (series, max_lag, note) VALUES
    ('load_QC',       interval '3 hours',  'Hydro-Quebec real-time feed, newest complete hour'),
    ('load_ON',       interval '6 hours',  'IESO Demand yearly file, refreshed through the day'),
    ('load_AB',       interval '6 hours',  'AESO Alberta Internal Load'),
    ('generation_ON', interval '30 hours', 'IESO GenOutputCapability daily report'),
    ('price_ON',      interval '12 hours', 'Day-ahead prices; usually already ahead of the clock, so lag is negative'),
    ('price_AB',      interval '12 hours', 'AESO pool price, settled hourly'),
    ('weather_QC',    interval '48 hours', 'ECCC archive trails real time by about a day'),
    ('weather_ON',    interval '48 hours', 'ECCC archive trails real time by about a day'),
    ('weather_AB',    interval '48 hours', 'ECCC archive trails real time by about a day');

-- +goose Down
DROP SCHEMA ops CASCADE;
