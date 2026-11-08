// Package loader reads raw bronze files and upserts typed rows into Postgres.
package loader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	Skipped bool // the same bytes were already loaded and force was not set
}

// statement is one parameterised SQL statement produced by a source's parser.
type statement struct {
	sql  string
	args []any
}

// source says where a feed lives in bronze and how to turn a file into SQL.
type source struct {
	bronzeSource string // bronze partition name; also the load_log source
	parse        func(key string, data []byte) ([]statement, error)
}

// SourceNames lists the feeds the loader knows, sorted.
func SourceNames() []string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func lookup(name string) (source, error) {
	src, ok := sources[name]
	if !ok {
		return source{}, fmt.Errorf("loader: unknown source %q (available: %v)", name, SourceNames())
	}
	return src, nil
}

// Load loads one bronze file. It is skipped when load_log shows the same bytes
// were already loaded for that key; a revised file at the same key is loaded
// again. Rows and the load_log entry commit together or not at all.
func (l *Loader) Load(ctx context.Context, name, key string, force bool) (Result, error) {
	src, err := lookup(name)
	if err != nil {
		return Result{}, err
	}
	data, err := l.Bronze.Get(ctx, key)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	if !force {
		var previous string
		err := l.Pool.QueryRow(ctx,
			`SELECT content_sha256 FROM staging.load_log WHERE source = $1 AND bronze_key = $2`,
			src.bronzeSource, key).Scan(&previous)
		switch {
		case err == nil && previous == hash:
			return Result{Key: key, Skipped: true}, nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return Result{}, fmt.Errorf("loader: check load_log: %w", err)
		}
	}

	stmts, err := src.parse(key, data)
	if err != nil {
		return Result{}, fmt.Errorf("loader: %s: %w", key, err)
	}

	tx, err := l.Pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("loader: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	batch := &pgx.Batch{}
	for _, s := range stmts {
		batch.Queue(s.sql, s.args...)
	}
	br := tx.SendBatch(ctx, batch)
	for range stmts {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return Result{}, fmt.Errorf("loader: upsert %s: %w", key, err)
		}
	}
	if err := br.Close(); err != nil {
		return Result{}, fmt.Errorf("loader: close batch %s: %w", key, err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO staging.load_log (source, bronze_key, row_count, content_sha256) VALUES ($1, $2, $3, $4)
ON CONFLICT (source, bronze_key) DO UPDATE SET
    row_count = EXCLUDED.row_count, content_sha256 = EXCLUDED.content_sha256, loaded_at = now()`,
		src.bronzeSource, key, len(stmts), hash); err != nil {
		return Result{}, fmt.Errorf("loader: write load_log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("loader: commit %s: %w", key, err)
	}
	return Result{Key: key, Rows: len(stmts)}, nil
}

// LoadAll loads every bronze file of one source, in key order. A failing file
// does not stop the rest; errors are joined and returned at the end.
func (l *Loader) LoadAll(ctx context.Context, name string, force bool) ([]Result, error) {
	src, err := lookup(name)
	if err != nil {
		return nil, err
	}
	keys, err := l.Bronze.List(ctx, "source="+src.bronzeSource+"/")
	if err != nil {
		return nil, err
	}
	var results []Result
	var errs []error
	for _, key := range keys {
		res, err := l.Load(ctx, name, key, force)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}
