DROP TABLE IF EXISTS gold.dim_province CASCADE;
CREATE TABLE gold.dim_province AS
SELECT * FROM (VALUES
    ('QC', 'Quebec',  'Québec',  'Hydro-Québec', 'hydroquebec'),
    ('ON', 'Ontario', 'Ontario', 'IESO',         'ieso'),
    ('AB', 'Alberta', 'Alberta',  'AESO',         'aeso')
) AS t(province_code, name_en, name_fr, grid_operator, source_system);
ALTER TABLE gold.dim_province ADD PRIMARY KEY (province_code);
