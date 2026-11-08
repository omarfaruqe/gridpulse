-- +goose Up
-- Outcome of every data quality test run, so the pipeline-health view can show
-- more than "the last log line". Pruned to 30 days by the transform step.
CREATE TABLE ops.test_result (
    id         bigserial   PRIMARY KEY,
    run_at     timestamptz NOT NULL DEFAULT now(),
    test       text        NOT NULL,
    violations integer     NOT NULL CHECK (violations >= 0),
    sample     jsonb       -- up to five offending rows; NULL when the test passed
);
CREATE INDEX test_result_test_run_idx ON ops.test_result (test, run_at DESC);

-- +goose Down
DROP TABLE ops.test_result;
