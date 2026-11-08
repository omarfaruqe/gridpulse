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
		WHERE table_schema = 'staging' AND table_name IN ('load_log', 'hq_demand_15min', 'ieso_generator_hourly', 'ieso_demand_hourly',
			'ieso_da_price_hourly', 'aeso_pool_price_hourly', 'aeso_load_hourly', 'eccc_weather_hourly')`).Scan(&tables)
	if err != nil || tables != 8 {
		t.Fatalf("staging tables = %d, err = %v, want 8", tables, err)
	}
}
