SELECT province_code, sum(weight) AS total_weight FROM gold.dim_station
GROUP BY province_code HAVING abs(sum(weight) - 1) > 0.001
