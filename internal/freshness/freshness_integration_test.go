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
	// Quebec: fresh (1 h old; limit 3 h). Ontario: stale (40 h old; limit 36 h).
	// Alberta: its newest row has a NULL value, so the series counts as having no data.
	exec(`INSERT INTO gold.fact_hourly_load (province_code, hour_utc, load_mw, is_complete, source) VALUES
	  ('QC', $1, 20000, true, 'x'), ('ON', $2, 15000, true, 'x'), ('AB', $3, NULL, false, 'x')`,
		now.Add(-time.Hour), now.Add(-40*time.Hour), now.Add(-time.Hour))
	// Ontario day-ahead prices run ahead of the clock: negative lag, still fresh.
	exec(`INSERT INTO gold.fact_hourly_price (province_code, hour_utc, price_cad_mwh, price_kind, source)
	  VALUES ('ON', $1, 40, 'day_ahead_zonal', 'x')`, now.Add(14*time.Hour))

	// Real-time prices stop 5 h ago (limit 3 h). The series is separate from the
	// day-ahead one, which stays fresh because it runs ahead of the clock.
	exec(`INSERT INTO gold.fact_hourly_price (province_code, hour_utc, price_cad_mwh, price_kind, source)
	  VALUES ('ON', $1, 50, 'real_time_zonal_avg', 'x')`, now.Add(-5*time.Hour))

	statuses, err := Check(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Status{}
	for _, s := range statuses {
		got[s.Series] = s
	}
	if len(got) != 10 {
		t.Fatalf("series = %d, want 10 from ops.freshness_sla", len(got))
	}
	want := map[string]bool{
		"load_QC": false, "load_ON": true, "load_AB": true,
		"price_ON": false, "price_AB": true, "price_rt_ON": true,
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

func TestRecordAndWorstLags(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	st := func(series string, lagHours int, hasData bool) Status {
		s := Status{Series: series, MaxLag: 3 * time.Hour}
		if hasData {
			l := now.Add(-time.Duration(lagHours) * time.Hour)
			s.LatestHour, s.Lag = &l, time.Duration(lagHours)*time.Hour
		}
		return s
	}

	// Checks over three hours: lag grows, then the series catches up.
	for i, lag := range []int{1, 4, 2} {
		at := now.Add(-time.Duration(2-i) * time.Hour)
		if err := Record(ctx, pool, at, []Status{st("load_QC", lag, true), st("load_AB", 0, false)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := WorstLags(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if got["load_QC"] != 4*3600 {
		t.Errorf("load_QC worst = %d, want %d", got["load_QC"], 4*3600)
	}
	if _, ok := got["load_AB"]; ok {
		t.Error("a series with no data has no lag to report")
	}

	// Rows older than 90 days are pruned by the next record.
	if _, err := pool.Exec(ctx, `INSERT INTO ops.freshness_lag (checked_at, series, lag_seconds, max_lag_seconds, stale)
		VALUES ($1, 'load_QC', 1, 1, false)`, now.AddDate(0, 0, -91)); err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, pool, now, []Status{st("load_QC", 1, true)}); err != nil {
		t.Fatal(err)
	}
	var old int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ops.freshness_lag WHERE checked_at < $1`, now.AddDate(0, 0, -90)).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if old != 0 {
		t.Errorf("%d rows older than 90 days remain", old)
	}
}
