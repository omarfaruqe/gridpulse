// Package db connects to Postgres and applies versioned schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Connect opens a connection pool and checks that the database answers.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return connect(ctx, dsn, false)
}

// ConnectReadOnly is Connect for programs that must never write, such as the
// API. Every session starts with default_transaction_read_only=on, so a bug in
// a query cannot change data. This is a safety net, not a security boundary:
// a session can switch the setting off, so production should also use a
// database role that has only SELECT.
func ConnectReadOnly(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return connect(ctx, dsn, true)
}

func connect(ctx context.Context, dsn string, readOnly bool) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse DSN: %w", err)
	}
	if readOnly {
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// Migrate applies every pending migration. It is safe to call repeatedly.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("db: migrations fs: %w", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub)
	if err != nil {
		return fmt.Errorf("db: create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("db: apply migrations: %w", err)
	}
	return nil
}
