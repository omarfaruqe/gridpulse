-- +goose Up
-- How far a month's IESO/StatCan ratio may sit from that group's own baseline
-- (the median ratio of the previous 12 comparable months) before the
-- reconciliation flags it. Units are ratio points: 0.02 is two percentage points.
-- PROVISIONAL: set before looking at the deviations; revised from the observed
-- history in a later migration.
CREATE TABLE ops.recon_tolerance (
    comparison_group text    PRIMARY KEY,
    max_deviation    numeric NOT NULL CHECK (max_deviation > 0),
    note             text
);

INSERT INTO ops.recon_tolerance (comparison_group, max_deviation, note) VALUES
    ('Nuclear',     0.02, 'provisional'),
    ('Total',       0.03, 'provisional'),
    ('Hydro',       0.06, 'provisional'),
    ('Wind',        0.08, 'provisional'),
    ('Solar',       0.10, 'provisional'),
    ('Combustible', 0.15, 'provisional');

-- +goose Down
DROP TABLE ops.recon_tolerance;
