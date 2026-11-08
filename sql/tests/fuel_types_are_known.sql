-- Schema drift: IESO introduced a fuel type that dim_fuel_type does not describe.
SELECT DISTINCT f.fuel_type FROM gold.fact_generation_by_fuel f
WHERE NOT EXISTS (SELECT 1 FROM gold.dim_fuel_type d WHERE d.fuel_type = f.fuel_type)
