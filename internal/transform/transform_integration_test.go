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
	// Ontario generation: one of two gas generators did not report.
	exec(t, pool, `INSERT INTO staging.ieso_generator_hourly (interval_start_utc, generator, fuel_type, hour_ending, energy_mw, bronze_key) VALUES
	 ('2026-11-06 05:00+00','A','GAS',1,100,'k'), ('2026-11-06 05:00+00','B','GAS',1,NULL,'k'),
	 ('2026-11-06 05:00+00','C','NUCLEAR',1,800,'k')`)
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
	q(`SELECT generation_mw, generators_reporting, generators_total, is_complete FROM gold.fact_generation_by_fuel WHERE fuel_type='GAS'`, &gen, &reporting, &total, &complete)
	if !near(gen, 100) || reporting != 1 || total != 2 || complete {
		t.Errorf("GAS = %v %d/%d complete=%v, want 100 1/2 false", gen, reporting, total, complete)
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
	exec(t, pool, `INSERT INTO staging.ieso_generator_hourly (interval_start_utc, generator, fuel_type, hour_ending, energy_mw, bronze_key)
	 VALUES ('2026-11-06 05:00+00','Z','FUSION',1,1,'k')`)

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
