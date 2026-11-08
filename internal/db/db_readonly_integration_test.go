//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/db"
)

func TestConnectReadOnlyRefusesWrites(t *testing.T) {
	ctx := context.Background()
	pool, err := db.ConnectReadOnly(ctx, config.Load().PostgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var one int
	if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("reads must work: %d, %v", one, err)
	}
	// Even a temporary table is refused in a read-only transaction, so this touches nothing.
	if _, err := pool.Exec(ctx, `CREATE TEMP TABLE should_not_exist (a int)`); err == nil {
		t.Error("write succeeded on a read-only pool")
	}
}
