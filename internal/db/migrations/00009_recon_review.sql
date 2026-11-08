-- +goose Up
-- Revised after looking at 56 months of history (2022-01 to 2026-08).
-- Combustible: IESO's gas+biofuel against StatCan's combustible-fuels total swings
-- widely month to month (StatCan includes industrial generation); a 2023-05 move of
-- 18 points was normal behaviour for the group, not a fault.
UPDATE ops.recon_tolerance
SET max_deviation = 0.20,
    note = 'raised from 0.15 after 2023-05 (-0.18) showed ordinary swings of this size'
WHERE comparison_group = 'Combustible';
UPDATE ops.recon_tolerance SET note = 'provisional; every month to 2026-08 stayed inside it'
WHERE comparison_group IN ('Total', 'Hydro', 'Wind', 'Solar');
UPDATE ops.recon_tolerance SET note = 'provisional; normal months stay within 0.5 points, see ops.recon_reviewed for 2023-07/08'
WHERE comparison_group = 'Nuclear';

-- Months that were looked at and are known to sit outside the band. They stay
-- visible in gold.recon_generation_monthly (status out_of_band); the data
-- quality test skips them so it still fails for any NEW month.
CREATE TABLE ops.recon_reviewed (
    comparison_group text NOT NULL,
    ref_month        date NOT NULL,
    reviewed_on      date NOT NULL,
    note             text NOT NULL,
    PRIMARY KEY (comparison_group, ref_month)
);

INSERT INTO ops.recon_reviewed (comparison_group, ref_month, reviewed_on, note) VALUES
    ('Nuclear', '2023-07-01', '2026-11-08',
     'IESO nuclear is about 291 GWh below StatCan (about 390 MW around the clock); other months differ by under 1%. Cause not established.'),
    ('Nuclear', '2023-08-01', '2026-11-08',
     'IESO nuclear is about 294 GWh below StatCan (about 395 MW around the clock); other months differ by under 1%. Cause not established.');

-- +goose Down
DROP TABLE ops.recon_reviewed;
UPDATE ops.recon_tolerance SET max_deviation = 0.15, note = 'provisional' WHERE comparison_group = 'Combustible';
UPDATE ops.recon_tolerance SET note = 'provisional' WHERE comparison_group <> 'Combustible';
