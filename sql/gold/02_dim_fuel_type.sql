-- is_renewable / is_low_carbon are NULL where IESO does not say what a category is.
-- CONTROL ACTIONS: appears in IESO's fuel report for 24 hours on 2026-07-15/16 with
-- output 0 (or none); it carries no energy, and is listed so it is a known value.
-- OTHER only appears from 2025-04.
DROP TABLE IF EXISTS gold.dim_fuel_type CASCADE;
CREATE TABLE gold.dim_fuel_type AS
SELECT * FROM (VALUES
    ('BIOFUEL', 'Biofuel',     'Biocarburant',    true,  true),
    ('GAS',     'Natural gas', 'Gaz naturel',     false, false),
    ('HYDRO',   'Hydro',       'Hydroélectrique', true,  true),
    ('NUCLEAR', 'Nuclear',     'Nucléaire',       false, true),
    ('SOLAR',   'Solar',       'Solaire',         true,  true),
    ('WIND',    'Wind',        'Éolien',          true,  true),
    ('OTHER',   'Other',       'Autre',           NULL::boolean, NULL::boolean),
    ('CONTROL ACTIONS', 'Control actions', 'Mesures de contrôle', NULL::boolean, NULL::boolean)
) AS t(fuel_type, name_en, name_fr, is_renewable, is_low_carbon);
ALTER TABLE gold.dim_fuel_type ADD PRIMARY KEY (fuel_type);
