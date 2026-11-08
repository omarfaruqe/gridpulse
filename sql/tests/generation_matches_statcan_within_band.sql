-- A month whose IESO/StatCan ratio left its group's usual range, or a group
-- without a tolerance. Either IESO's data, StatCan's data or the mapping changed.
-- Months listed in ops.recon_reviewed were looked at and are skipped here (they
-- remain out_of_band in the table).
SELECT g.ref_month, g.comparison_group, round(g.ratio::numeric, 4) AS ratio,
       round(g.baseline_ratio::numeric, 4) AS baseline_ratio, g.tolerance, g.status
FROM gold.recon_generation_monthly g
WHERE g.status IN ('out_of_band', 'no_tolerance')
  AND NOT EXISTS (SELECT 1 FROM ops.recon_reviewed r
                   WHERE r.comparison_group = g.comparison_group AND r.ref_month = g.ref_month)
