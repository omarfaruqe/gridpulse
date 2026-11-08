-- is_renewable / is_low_carbon are NULL for OTHER: IESO does not say what it is.
DROP TABLE IF EXISTS gold.dim_fuel_type CASCADE;
CREATE TABLE gold.dim_fuel_type AS
SELECT * FROM (VALUES
    ('BIOFUEL', 'Biofuel',     'Biocarburant',    true,  true),
    ('GAS',     'Natural gas', 'Gaz naturel',     false, false),
    ('HYDRO',   'Hydro',       'Hydroélectrique', true,  true),
    ('NUCLEAR', 'Nuclear',     'Nucléaire',       false, true),
    ('SOLAR',   'Solar',       'Solaire',         true,  true),
    ('WIND',    'Wind',        'Éolien',          true,  true),
    ('OTHER',   'Other',       'Autre',           NULL::boolean, NULL::boolean)
) AS t(fuel_type, name_en, name_fr, is_renewable, is_low_carbon);
ALTER TABLE gold.dim_fuel_type ADD PRIMARY KEY (fuel_type);
