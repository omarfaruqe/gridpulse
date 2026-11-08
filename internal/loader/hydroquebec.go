// Package loader reads raw bronze files and upserts typed rows into Postgres.
package loader

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/extractors/hydroquebec"
)

// BronzeReader is the part of the bronze store the loader needs.
type BronzeReader interface {
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
}

// Loader moves bronze files into the staging schema.
type Loader struct {
	Pool   *pgxpool.Pool
	Bronze BronzeReader
}

// Result describes what happened to one bronze file.
type Result struct {
	Key     string
	Rows    int
	Skipped bool // already loaded and force was not set
}

// SourceHydroQuebec is the load_log source name and bronze partition.
const SourceHydroQuebec = "hydroquebec_demand"

// The WHERE clause keeps a replayed older snapshot from overwriting newer data,
// so backfills and re-runs in any order end in the same state.
const upsertHydroQuebec = `
INSERT INTO staging.hq_demand_15min (time_utc, local_time, demand_mw, time_resolution, snapshot_utc, bronze_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (time_utc) DO UPDATE SET
    local_time      = EXCLUDED.local_time,
    demand_mw       = EXCLUDED.demand_mw,
    time_resolution = EXCLUDED.time_resolution,
    snapshot_utc    = EXCLUDED.snapshot_utc,
    bronze_key      = EXCLUDED.bronze_key,
    loaded_at       = now()
WHERE EXCLUDED.snapshot_utc >= staging.hq_demand_15min.snapshot_utc`

// LoadHydroQuebec loads one bronze file. Rows and the load_log entry commit
// together or not at all.
func (l *Loader) LoadHydroQuebec(ctx context.Context, key string, force bool) (Result, error) {
	if !force {
		done, err := l.alreadyLoaded(ctx, SourceHydroQuebec, key)
		if err != nil {
			return Result{}, err
		}
		if done {
			return Result{Key: key, Skipped: true}, nil
		}
	}

	data, err := l.Bronze.Get(ctx, key)
	if err != nil {
		return Result{}, err
	}
	obs, err := hydroquebec.Parse(data)
	if err != nil {
		return Result{}, fmt.Errorf("loader: %s: %w", key, err)
	}
	snapshot, err := hydroquebec.SnapshotFromKey(key)
	if err != nil {
		return Result{}, err
	}

	tx, err := l.Pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("loader: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	batch := &pgx.Batch{}
	for _, o := range obs {
		batch.Queue(upsertHydroQuebec, o.TimeUTC, o.LocalTime, o.DemandMW, o.Resolution.String(), snapshot, key)
	}
	br := tx.SendBatch(ctx, batch)
	for range obs {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return Result{}, fmt.Errorf("loader: upsert %s: %w", key, err)
		}
	}
	if err := br.Close(); err != nil {
		return Result{}, fmt.Errorf("loader: close batch %s: %w", key, err)
	}
	if err := logLoad(ctx, tx, SourceHydroQuebec, key, len(obs)); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("loader: commit %s: %w", key, err)
	}
	return Result{Key: key, Rows: len(obs)}, nil
}

// LoadAllHydroQuebec loads every Hydro-Quebec file in bronze, in key order. A
// failing file does not stop the rest; errors are joined and returned at the end.
func (l *Loader) LoadAllHydroQuebec(ctx context.Context, force bool) ([]Result, error) {
	keys, err := l.Bronze.List(ctx, "source="+SourceHydroQuebec+"/")
	if err != nil {
		return nil, err
	}
	var results []Result
	var errs []error
	for _, key := range keys {
		res, err := l.LoadHydroQuebec(ctx, key, force)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}

func (l *Loader) alreadyLoaded(ctx context.Context, source, key string) (bool, error) {
	var exists bool
	err := l.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM staging.load_log WHERE source = $1 AND bronze_key = $2)`,
		source, key).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("loader: check load_log: %w", err)
	}
	return exists, nil
}

func logLoad(ctx context.Context, tx pgx.Tx, source, key string, rows int) error {
	_, err := tx.Exec(ctx, `
INSERT INTO staging.load_log (source, bronze_key, row_count) VALUES ($1, $2, $3)
ON CONFLICT (source, bronze_key) DO UPDATE SET row_count = EXCLUDED.row_count, loaded_at = now()`,
		source, key, rows)
	if err != nil {
		return fmt.Errorf("loader: write load_log: %w", err)
	}
	return nil
}
