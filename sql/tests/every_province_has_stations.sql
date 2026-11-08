SELECT p.province_code FROM gold.dim_province p
WHERE NOT EXISTS (SELECT 1 FROM gold.dim_station s WHERE s.province_code = p.province_code)
