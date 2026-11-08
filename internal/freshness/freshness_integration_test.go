//go:build integration

package freshness

import (
	"context"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/db/dbtest"
	"github.com/omarfaruqe/gridpulse/internal/transform"
	sqlfiles "github.com/omarfaruqe/gridpulse/sql"
)

func TestCheck(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	// Building on empty staging creates the (empty) gold tables.
	if err := (&transform.Runner{Pool: pool, Files: sqlfiles.FS}).Build(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Quebec: fresh (1 h old; limit 3 h). Ontario: stale (10 h old; limit 6 h).
	// Alberta: its newest row has a NULL value, so the series counts as having no data.
	exec(`INSERT INTO gold.fact_hourly_load (province_code, hour_utc, load_mw, is_complete, source) VALUES
	  ('QC', $1, 20000, true, 'x'), ('ON', $2, 15000, true, 'x'), ('AB', $3, NULL, false, 'x')`,
		now.Add(-time.Hour), now.Add(-10*time.Hour), now.Add(-time.Hour))
	// Ontario day-ahead prices run ahead of the clock: negative lag, still fresh.
	exec(`INSERT INTO gold.fact_hourly_price (province_code, hour_utc, price_cad_mwh, price_kind, source)
	  VALUES ('ON', $1, 40, 'day_ahead_zonal', 'x')`, now.Add(14*time.Hour))

	statuses, err := Check(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Status{}
	for _, s := range statuses {
		got[s.Series] = s
	}
	if len(got) != 9 {
		t.Fatalf("series = %d, want 9 from ops.freshness_sla", len(got))
	}
	want := map[string]bool{
		"load_QC": false, "load_ON": true, "load_AB": true,
		"price_ON": false, "price_AB": true,
		"generation_ON": true, "weather_QC": true, "weather_ON": true, "weather_AB": true,
	}
	for series, stale := range want {
		if got[series].Stale != stale {
			t.Errorf("%s stale = %v, want %v (%+v)", series, got[series].Stale, stale, got[series])
		}
	}
	if got["load_QC"].Lag != time.Hour {
		t.Errorf("load_QC lag = %v, want 1h", got["load_QC"].Lag)
	}
	if got["price_ON"].Lag >= 0 {
		t.Errorf("price_ON lag = %v, want negative (ahead of the clock)", got["price_ON"].Lag)
	}
	if got["load_AB"].LatestHour != nil {
		t.Errorf("load_AB latest = %v, want nil (only a NULL value exists)", got["load_AB"].LatestHour)
	}
	if got["load_QC"].MaxLag != 3*time.Hour {
		t.Errorf("load_QC max lag = %v, want 3h", got["load_QC"].MaxLag)
	}
}
