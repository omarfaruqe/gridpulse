//go:build integration

package transform

import (
	"context"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/db/dbtest"
	sqlfiles "github.com/omarfaruqe/gridpulse/sql"
)

func exec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// Quebec: hour 05:00 has all four slots; hour 06:00 is missing one.
	exec(t, pool, `INSERT INTO staging.hq_demand_15min (time_utc, local_time, demand_mw, time_resolution, snapshot_utc, bronze_key) VALUES
	 ('2026-11-06 05:00+00','x',20000,'unique',now(),'k'), ('2026-11-06 05:15+00','x',21000,'unique',now(),'k'),
	 ('2026-11-06 05:30+00','x',22000,'unique',now(),'k'), ('2026-11-06 05:45+00','x',23000,'unique',now(),'k'),
	 ('2026-11-06 06:00+00','x',24000,'unique',now(),'k'), ('2026-11-06 06:15+00','x',NULL,'unique',now(),'k'),
	 ('2026-11-06 06:30+00','x',26000,'unique',now(),'k'), ('2026-11-06 06:45+00','x',27000,'unique',now(),'k')`)
	exec(t, pool, `INSERT INTO staging.ieso_demand_hourly (interval_start_utc, hour_ending, market_demand_mw, ontario_demand_mw, snapshot_utc, bronze_key)
	 VALUES ('2026-11-06 05:00+00',1,16000,15000,now(),'k')`)
	exec(t, pool, `INSERT INTO staging.aeso_load_hourly (interval_start_utc, load_mw, forecast_load_mw, bronze_key)
	 VALUES ('2026-11-06 05:00+00',10000,10100,'k')`)
	// Ontario generation by fuel: gas has no Output this hour.
	exec(t, pool, `INSERT INTO staging.ieso_fuel_hourly (interval_start_utc, fuel_type, hour_ending, output_mw, output_quality, snapshot_utc, bronze_key) VALUES
	 ('2026-11-06 05:00+00','GAS',1,NULL,3,now(),'k'), ('2026-11-06 05:00+00','NUCLEAR',1,800,0,now(),'k')`)
	// Montreal (weight 0.84) and Quebec City (0.16).
	exec(t, pool, `INSERT INTO staging.eccc_weather_hourly (climate_id, time_utc, station_name, province, temp, bronze_key) VALUES
	 ('7025251','2026-11-06 05:00+00','MONTREAL','QC',-10,'k'), ('7016293','2026-11-06 05:00+00','QUEBEC','QC',10,'k'),
	 ('7025251','2026-11-06 06:00+00','MONTREAL','QC',5,'k'),   ('7016293','2026-11-06 06:00+00','QUEBEC','QC',NULL,'k')`)
}

func near(got *float64, want float64) bool { return got != nil && math.Abs(*got-want) < 1e-9 }

func TestBuildAndTests(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	seed(t, pool)
	r := &Runner{Pool: pool, Files: sqlfiles.FS}

	for i := 0; i < 2; i++ { // building twice must give the same result
		if err := r.Build(ctx); err != nil {
			t.Fatalf("build %d: %v", i+1, err)
		}
	}

	var load *float64
	var complete bool
	q := func(sql string, dest ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql).Scan(dest...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q(`SELECT load_mw, is_complete FROM gold.fact_hourly_load WHERE province_code='QC' AND hour_utc='2026-11-06 05:00+00'`, &load, &complete)
	if !near(load, 21500) || !complete {
		t.Errorf("QC 05:00 = %v complete=%v, want 21500 true", load, complete)
	}
	q(`SELECT load_mw, is_complete FROM gold.fact_hourly_load WHERE province_code='QC' AND hour_utc='2026-11-06 06:00+00'`, &load, &complete)
	if load != nil || complete {
		t.Errorf("QC 06:00 = %v complete=%v, want NULL false (one slot missing)", load, complete)
	}
	q(`SELECT load_mw FROM gold.fact_hourly_load WHERE province_code='ON'`, &load)
	if !near(load, 15000) {
		t.Errorf("ON load = %v, want Ontario demand 15000, not market demand", load)
	}

	var gen *float64
	var reporting, total int
	q(`SELECT generation_mw, is_complete FROM gold.fact_generation_by_fuel WHERE fuel_type='GAS'`, &gen, &complete)
	if gen != nil || complete {
		t.Errorf("GAS = %v complete=%v, want NULL false (no Output published)", gen, complete)
	}
	q(`SELECT generation_mw, is_complete FROM gold.fact_generation_by_fuel WHERE fuel_type='NUCLEAR'`, &gen, &complete)
	if !near(gen, 800) || !complete {
		t.Errorf("NUCLEAR = %v complete=%v, want 800 true", gen, complete)
	}

	var temp *float64
	q(`SELECT temp_c, stations_reporting, stations_total, is_complete FROM gold.fact_hourly_weather WHERE hour_utc='2026-11-06 05:00+00'`, &temp, &reporting, &total, &complete)
	if !near(temp, 0.84*-10+0.16*10) || reporting != 2 || !complete {
		t.Errorf("weighted temp = %v %d/%d complete=%v, want -6.8 2/2 true", temp, reporting, total, complete)
	}
	q(`SELECT temp_c, stations_reporting, stations_total, is_complete FROM gold.fact_hourly_weather WHERE hour_utc='2026-11-06 06:00+00'`, &temp, &reporting, &total, &complete)
	if !near(temp, 5) || reporting != 1 || total != 2 || complete {
		t.Errorf("one-station temp = %v %d/%d complete=%v, want 5 1/2 false (weights renormalised)", temp, reporting, total, complete)
	}

	var mart int
	q(`SELECT count(*) FROM gold.mart_load_weather`, &mart)
	if mart != 1 {
		t.Errorf("mart rows = %d, want 1 (only QC 05:00 has both load and temperature)", mart)
	}

	results, err := r.Test(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range results {
		if !res.Passed() {
			t.Errorf("test %s failed on clean data: %d violations %v", res.Name, res.Violations, res.Sample)
		}
	}
}

func TestDataQualityTestsCatchProblems(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	seed(t, pool)
	// A negative load, a gap (05:00 and 08:00 present, 06:00-07:00 absent) and an unknown fuel.
	exec(t, pool, `INSERT INTO staging.ieso_demand_hourly (interval_start_utc, hour_ending, market_demand_mw, ontario_demand_mw, snapshot_utc, bronze_key)
	 VALUES ('2026-11-06 08:00+00',4,1,-5,now(),'k')`)
	exec(t, pool, `INSERT INTO staging.ieso_fuel_hourly (interval_start_utc, fuel_type, hour_ending, output_mw, output_quality, snapshot_utc, bronze_key)
	 VALUES ('2026-11-06 05:00+00','FUSION',1,1,0,now(),'k')`)

	r := &Runner{Pool: pool, Files: sqlfiles.FS}
	if err := r.Build(ctx); err != nil {
		t.Fatal(err)
	}
	results, err := r.Test(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"no_negative_load": 1, "no_missing_hours_load": 2, "fuel_types_are_known": 1}
	for _, res := range results {
		if n, ok := want[res.Name]; ok {
			if res.Violations != n {
				t.Errorf("%s violations = %d, want %d (%v)", res.Name, res.Violations, n, res.Sample)
			}
			delete(want, res.Name)
		}
	}
	for name := range want {
		t.Errorf("test %s did not run", name)
	}
}

func TestReconciliationBaselineAndStatuses(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	// Nuclear at a steady 1000 MW for every hour of Jan-Sep 2025 (IESO months are fixed EST).
	exec(t, pool, `INSERT INTO staging.ieso_fuel_hourly (interval_start_utc, fuel_type, hour_ending, output_mw, output_quality, snapshot_utc, bronze_key)
	 SELECT t, 'NUCLEAR', 1, 1000, 0, now(), 'k'
	 FROM generate_series('2025-01-01 05:00+00'::timestamptz, '2025-10-01 04:00+00', interval '1 hour') t`)
	// March loses 10 hours (1.3% of the month): not comparable with a full month.
	exec(t, pool, `DELETE FROM staging.ieso_fuel_hourly
	 WHERE interval_start_utc >= '2025-03-01 05:00+00' AND interval_start_utc < '2025-03-01 15:00+00'`)
	// StatCan reports 99% coverage every month, except September where IESO would be at 80%.
	exec(t, pool, `INSERT INTO staging.statcan_generation_monthly (ref_month, geo, producer_class, generation_type, mwh, status, vector, snapshot_utc, bronze_key)
	 SELECT m::date, 'Ontario', 'Total all classes of electricity producer', 'Nuclear steam turbine',
	        ((m::date + interval '1 month')::date - m::date) * 24 * 1000 / CASE WHEN m = '2025-09-01' THEN 0.80 ELSE 0.99 END,
	        NULL, 'v1', now(), 'k'
	 FROM generate_series('2025-01-01'::date, '2025-09-01', interval '1 month') m`)

	if err := (&Runner{Pool: pool, Files: sqlfiles.FS}).Build(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"2025-01": "no_baseline", "2025-02": "no_baseline", "2025-03": "incomplete",
		"2025-04": "no_baseline", "2025-05": "no_baseline", "2025-06": "no_baseline", "2025-07": "no_baseline",
		"2025-08": "ok", // six comparable months before it (March is excluded)
		"2025-09": "out_of_band",
	}
	rows, err := pool.Query(ctx, `SELECT to_char(ref_month, 'YYYY-MM'), status FROM gold.recon_generation_monthly WHERE comparison_group = 'Nuclear'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var month, status string
		if err := rows.Scan(&month, &status); err != nil {
			t.Fatal(err)
		}
		got[month] = status
	}
	for month, status := range want {
		if got[month] != status {
			t.Errorf("%s = %q, want %q", month, got[month], status)
		}
	}
	if len(got) != len(want) {
		t.Errorf("months = %d, want %d", len(got), len(want))
	}

	var ratio, baseline, tolerance *float64
	if err := pool.QueryRow(ctx, `SELECT ratio::float8, baseline_ratio::float8, tolerance::float8
	   FROM gold.recon_generation_monthly WHERE comparison_group = 'Nuclear' AND ref_month = '2025-08-01'`).
		Scan(&ratio, &baseline, &tolerance); err != nil {
		t.Fatal(err)
	}
	if ratio == nil || math.Abs(*ratio-0.99) > 1e-9 {
		t.Errorf("ratio = %v, want 0.99", ratio)
	}
	if baseline == nil || math.Abs(*baseline-0.99) > 1e-9 || tolerance == nil || *tolerance != 0.02 {
		t.Errorf("baseline = %v tolerance = %v, want 0.99 and 0.02", baseline, tolerance)
	}

	// The data quality test fires on the out-of-band month, and only on that one.
	bandViolations := func() int {
		t.Helper()
		results, err := (&Runner{Pool: pool, Files: sqlfiles.FS}).Test(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, res := range results {
			if res.Name == "generation_matches_statcan_within_band" {
				return res.Violations
			}
		}
		t.Fatal("band test did not run")
		return -1
	}
	if n := bandViolations(); n != 1 {
		t.Errorf("band test violations = %d, want 1", n)
	}

	// Recording the month as reviewed stops the test failing on it, but the
	// table still shows the month as out_of_band.
	exec(t, pool, `INSERT INTO ops.recon_reviewed (comparison_group, ref_month, reviewed_on, note)
	 VALUES ('Nuclear', '2025-09-01', '2026-11-08', 'test')`)
	if n := bandViolations(); n != 0 {
		t.Errorf("band test violations after review = %d, want 0", n)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM gold.recon_generation_monthly
	   WHERE comparison_group = 'Nuclear' AND ref_month = '2025-09-01'`).Scan(&status); err != nil || status != "out_of_band" {
		t.Errorf("status after review = %q, %v, want it to stay out_of_band", status, err)
	}
}

func TestReconciliationCompletenessIgnoresSporadicFuels(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	// All six core fuels for every hour of 2025-01, plus a sporadic OTHER and CONTROL ACTIONS.
	for _, fuel := range []string{"HYDRO", "NUCLEAR", "WIND", "SOLAR", "GAS", "BIOFUEL"} {
		exec(t, pool, `INSERT INTO staging.ieso_fuel_hourly (interval_start_utc, fuel_type, hour_ending, output_mw, output_quality, snapshot_utc, bronze_key)
		 SELECT t, '`+fuel+`', 1, 100, 0, now(), 'k'
		 FROM generate_series('2025-01-01 05:00+00'::timestamptz, '2025-02-01 04:00+00', interval '1 hour') t`)
	}
	exec(t, pool, `INSERT INTO staging.ieso_fuel_hourly (interval_start_utc, fuel_type, hour_ending, output_mw, output_quality, snapshot_utc, bronze_key) VALUES
	 ('2025-01-10 05:00+00', 'OTHER', 1, 50, 0, now(), 'k'), ('2025-01-10 06:00+00', 'CONTROL ACTIONS', 2, NULL, 0, now(), 'k')`)
	exec(t, pool, `INSERT INTO staging.statcan_generation_monthly (ref_month, geo, producer_class, generation_type, mwh, status, vector, snapshot_utc, bronze_key)
	 VALUES ('2025-01-01', 'Ontario', 'Total all classes of electricity producer', 'Total all types of electricity generation', 5000000, NULL, 'v1', now(), 'k')`)
	if err := (&Runner{Pool: pool, Files: sqlfiles.FS}).Build(ctx); err != nil {
		t.Fatal(err)
	}
	var coverage, ieso float64
	if err := pool.QueryRow(ctx, `SELECT ieso_coverage::float8, ieso_mwh FROM gold.recon_generation_monthly WHERE comparison_group = 'Total'`).
		Scan(&coverage, &ieso); err != nil {
		t.Fatal(err)
	}
	if coverage != 1 {
		t.Errorf("coverage = %v, want 1: sporadic fuels must not count as missing data", coverage)
	}
	if want := float64(6*744*100 + 50); ieso != want {
		t.Errorf("total energy = %v, want %v (OTHER's output is included)", ieso, want)
	}
}
