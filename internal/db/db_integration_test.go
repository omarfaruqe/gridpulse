//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/omarfaruqe/gridpulse/internal/db"
	"github.com/omarfaruqe/gridpulse/internal/db/dbtest"
)

func TestMigrateCreatesStagingAndIsRepeatable(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t) // already migrated once

	if err := db.Migrate(ctx, pool); err != nil { // a second run must be a no-op
		t.Fatalf("second Migrate: %v", err)
	}

	var tables int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'staging' AND table_name IN ('load_log', 'hq_demand_15min')`).Scan(&tables)
	if err != nil || tables != 2 {
		t.Fatalf("staging tables = %d, err = %v, want 2", tables, err)
	}
}
