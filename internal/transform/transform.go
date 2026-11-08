// Package transform builds the silver and gold layers from staging and runs
// the SQL data quality tests against the result.
package transform

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/extractors/eccc"
)

// Runner executes SQL files from Files, which holds silver/, gold/ and tests/.
type Runner struct {
	Pool  *pgxpool.Pool
	Files fs.FS
}

// TestResult is the outcome of one data quality test.
type TestResult struct {
	Name       string
	Violations int
	Sample     []string // up to 5 offending rows as JSON
}

// Passed reports whether the test found no offending rows.
func (t TestResult) Passed() bool { return t.Violations == 0 }

// Build refreshes the station mapping and rebuilds silver and gold in one
// transaction, so readers see either the old layers or the new, never a mix.
func (r *Runner) Build(ctx context.Context) error {
	stations, err := eccc.Stations()
	if err != nil {
		return err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("transform: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(ctx, `TRUNCATE staging.station_region`); err != nil {
		return fmt.Errorf("transform: clear station_region: %w", err)
	}
	for _, s := range stations {
		if _, err := tx.Exec(ctx, `
INSERT INTO staging.station_region (climate_id, name, province, region, operator, weight)
VALUES ($1, $2, $3, $4, $5, $6)`,
			s.ClimateID, s.Name, s.Province, s.Region, s.Operator, s.Weight); err != nil {
			return fmt.Errorf("transform: seed station %s: %w", s.ClimateID, err)
		}
	}

	for _, dir := range []string{"silver", "gold"} {
		files, err := fs.Glob(r.Files, dir+"/*.sql")
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("transform: no SQL files found in %s/", dir)
		}
		for _, f := range files { // Glob returns names sorted, so numeric prefixes set the order
			body, err := fs.ReadFile(r.Files, f)
			if err != nil {
				return err
			}
			// No arguments: pgx uses the simple protocol, so a file may hold several statements.
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return fmt.Errorf("transform: %s: %w", f, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("transform: commit: %w", err)
	}
	return nil
}

// Test runs every tests/*.sql. A test is a query that returns the rows that
// break a rule. An error is returned only when a test cannot be executed.
func (r *Runner) Test(ctx context.Context) ([]TestResult, error) {
	files, err := fs.Glob(r.Files, "tests/*.sql")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("transform: no tests found")
	}
	var results []TestResult
	for _, f := range files {
		body, err := fs.ReadFile(r.Files, f)
		if err != nil {
			return nil, err
		}
		q := strings.TrimRight(strings.TrimSpace(string(body)), "; \n")
		res := TestResult{Name: strings.TrimSuffix(path.Base(f), ".sql")}

		if err := r.Pool.QueryRow(ctx, "SELECT count(*) FROM ("+q+") AS violations").Scan(&res.Violations); err != nil {
			return nil, fmt.Errorf("transform: test %s: %w", res.Name, err)
		}
		if res.Violations > 0 {
			rows, err := r.Pool.Query(ctx, "SELECT row_to_json(v)::text FROM ("+q+") AS v LIMIT 5")
			if err != nil {
				return nil, fmt.Errorf("transform: sample %s: %w", res.Name, err)
			}
			res.Sample, err = pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return nil, fmt.Errorf("transform: sample %s: %w", res.Name, err)
			}
		}
		results = append(results, res)
	}
	return results, nil
}

// Record stores the outcome of a test run in ops.test_result and prunes
// results older than 30 days. Test results are history, so a failure to record
// them is returned but must not hide the results themselves.
func (r *Runner) Record(ctx context.Context, results []TestResult) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("transform: record: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	for _, res := range results {
		var sample any // NULL for a passing test; each element is row_to_json output
		if len(res.Sample) > 0 {
			sample = "[" + strings.Join(res.Sample, ",") + "]"
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ops.test_result (test, violations, sample) VALUES ($1, $2, $3::text::jsonb)`,
			res.Name, res.Violations, sample); err != nil {
			return fmt.Errorf("transform: record %s: %w", res.Name, err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ops.test_result WHERE run_at < now() - interval '30 days'`); err != nil {
		return fmt.Errorf("transform: prune test results: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("transform: record: commit: %w", err)
	}
	return nil
}
