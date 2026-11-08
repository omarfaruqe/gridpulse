// Package dbtest gives integration tests a fresh, migrated Postgres database.
package dbtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/db"
)

// New creates a throwaway database on the configured Postgres server, applies
// all migrations, and drops the database when the test ends. The development
// database is never touched.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(config.Load().PostgresDSN)
	if err != nil {
		t.Fatalf("dbtest: parse DSN: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("dbtest: connect: %v", err)
	}

	name := fmt.Sprintf("gridpulse_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatalf("dbtest: create database: %v", err)
	}

	testCfg := cfg.Copy()
	testCfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatalf("dbtest: connect to %s: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
	return pool
}
