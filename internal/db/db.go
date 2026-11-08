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
	pool, err := pgxpool.New(ctx, dsn)
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
