//go:build integration

package loader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/db/dbtest"
)

func fixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "extractors"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func utc(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }

func floatAt(t *testing.T, l *Loader, sql string, args ...any) *float64 {
	t.Helper()
	var v *float64
	if err := l.Pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s %v: %v", sql, args, err)
	}
	return v
}

func textAt(t *testing.T, l *Loader, sql string, args ...any) *string {
	t.Helper()
	var v *string
	if err := l.Pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s %v: %v", sql, args, err)
	}
	return v
}

func wantFloat(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func wantNull(t *testing.T, what string, got *float64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %v, want NULL", what, *got)
	}
}

const (
	genKey    = "source=ieso_genoutputcapability/date=2026-11-06/PUB_GenOutputCapability_20261106.xml"
	demandKey = "source=ieso_demand/date=2026-11-07/PUB_Demand_2026_20261107T123015Z.csv"
	priceKey  = "source=ieso_dahourlyontariozonalprice/date=2026-11-06/PUB_DAHourlyOntarioZonalPrice_20261106.xml"
	poolKey   = "source=aeso_poolprice/date=2026-11-06/PoolPrice_2026-11-06.json"
	aesoLoad  = "source=aeso_albertainternalload/date=2026-11-06/AlbertaInternalLoad_2026-11-06.json"
	ecccKey   = "source=eccc_climate_hourly/date=2026-11-06/7025251_2026-11-06.json"
)

func TestLoadGeneratorsPicksUpRevisedFileAtSameKey(t *testing.T) {
	ctx := context.Background()
	original := fixture(t, "ieso", "testdata", "GenOutputCapability.xml")
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{genKey: original}}

	res, err := l.Load(ctx, "ieso-generation", genKey, false)
	if err != nil || res.Skipped || res.Rows != 6 {
		t.Fatalf("first load = %+v, %v", res, err)
	}
	hour1 := utc(2026, 11, 6, 5) // hour 1 of 2026-11-06 is 00:00 EST
	wantFloat(t, "BRUCEA-G1 hour 1", floatAt(t, l,
		`SELECT energy_mw FROM staging.ieso_generator_hourly WHERE interval_start_utc = $1 AND generator = 'BRUCEA-G1'`, hour1), 770)
	wantNull(t, "SAUNDERS-G1 hour 2 (no value published)", floatAt(t, l,
		`SELECT energy_mw FROM staging.ieso_generator_hourly WHERE interval_start_utc = $1 AND generator = 'SAUNDERS-G1'`, utc(2026, 11, 6, 6)))

	// Same bytes again: skipped.
	if res, err = l.Load(ctx, "ieso-generation", genKey, false); err != nil || !res.Skipped {
		t.Fatalf("reload of unchanged file = %+v, %v, want skipped", res, err)
	}

	// IESO revises the day: same key, new bytes. It must be loaded, not skipped.
	l.Bronze.(memBronze)[genKey] = []byte(strings.Replace(string(original), "<EnergyMW>770</EnergyMW>", "<EnergyMW>775</EnergyMW>", 1))
	res, err = l.Load(ctx, "ieso-generation", genKey, false)
	if err != nil || res.Skipped {
		t.Fatalf("revised file = %+v, %v, want loaded", res, err)
	}
	wantFloat(t, "BRUCEA-G1 hour 1 after revision", floatAt(t, l,
		`SELECT energy_mw FROM staging.ieso_generator_hourly WHERE interval_start_utc = $1 AND generator = 'BRUCEA-G1'`, hour1), 775)
	if n := countRows(t, l, "staging.ieso_generator_hourly"); n != 6 {
		t.Errorf("rows = %d, want 6 (no duplicates)", n)
	}
	if n := countRows(t, l, "staging.load_log"); n != 1 {
		t.Errorf("load_log rows = %d, want 1", n)
	}
}

func TestLoadIESODemandNewestSnapshotWins(t *testing.T) {
	ctx := context.Background()
	newer := fixture(t, "ieso", "testdata", "Demand.csv") // Created at 2026-11-07 07:30:15
	older := []byte("\\\\Hourly Demand Report,,,\n\\\\Created at 2026-11-06 07:30:15,,,\n\\\\For 2026,,,\n" +
		"Date,Hour,Market Demand,Ontario Demand\n2026-01-01,1,1,1\n")
	olderKey := "source=ieso_demand/date=2026-11-06/PUB_Demand_2026_20261106T123015Z.csv"

	for name, order := range map[string][]string{"old then new": {olderKey, demandKey}, "new then old": {demandKey, olderKey}} {
		t.Run(name, func(t *testing.T) {
			l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{demandKey: newer, olderKey: older}}
			for _, key := range order {
				if _, err := l.Load(ctx, "ieso-demand", key, false); err != nil {
					t.Fatal(err)
				}
			}
			hour1 := utc(2026, 1, 1, 5)
			wantFloat(t, "Ontario demand", floatAt(t, l, `SELECT ontario_demand_mw FROM staging.ieso_demand_hourly WHERE interval_start_utc = $1`, hour1), 16526)
			wantFloat(t, "Market demand", floatAt(t, l, `SELECT market_demand_mw FROM staging.ieso_demand_hourly WHERE interval_start_utc = $1`, hour1), 19489)
			wantNull(t, "blank hour 3", floatAt(t, l, `SELECT ontario_demand_mw FROM staging.ieso_demand_hourly WHERE interval_start_utc = $1`, utc(2026, 1, 1, 7)))
			if n := countRows(t, l, "staging.ieso_demand_hourly"); n != 3 {
				t.Errorf("rows = %d, want 3", n)
			}
		})
	}
}

func TestLoadIESOPrice(t *testing.T) {
	ctx := context.Background()
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{priceKey: fixture(t, "ieso", "testdata", "DAHourlyOntarioZonalPrice.xml")}}
	if _, err := l.Load(ctx, "ieso-price", priceKey, false); err != nil {
		t.Fatal(err)
	}
	wantFloat(t, "hour 1 price", floatAt(t, l, `SELECT zonal_price FROM staging.ieso_da_price_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 5)), 38.71)
	wantNull(t, "hour 3 price (blank, not zero)", floatAt(t, l, `SELECT zonal_price FROM staging.ieso_da_price_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 7)))
	if f := textAt(t, l, `SELECT flag FROM staging.ieso_da_price_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 5)); f == nil || *f != "DSO-RD" {
		t.Errorf("hour 1 flag = %v, want DSO-RD", f)
	}
	if f := textAt(t, l, `SELECT flag FROM staging.ieso_da_price_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 7)); f != nil {
		t.Errorf("hour 3 flag = %q, want NULL", *f)
	}
}

func TestLoadAESO(t *testing.T) {
	ctx := context.Background()
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{
		poolKey:  fixture(t, "aeso", "testdata", "PoolPrice.json"),
		aesoLoad: fixture(t, "aeso", "testdata", "AlbertaInternalLoad.json"),
	}}
	if _, err := l.Load(ctx, "aeso-price", poolKey, false); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Load(ctx, "aeso-load", aesoLoad, false); err != nil {
		t.Fatal(err)
	}
	wantFloat(t, "pool price", floatAt(t, l, `SELECT pool_price FROM staging.aeso_pool_price_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 6)), 10.51)
	wantNull(t, "unsettled pool price", floatAt(t, l, `SELECT pool_price FROM staging.aeso_pool_price_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 8)))
	wantFloat(t, "forecast pool price", floatAt(t, l, `SELECT forecast_pool_price FROM staging.aeso_pool_price_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 8)), 10.22)
	wantFloat(t, "load", floatAt(t, l, `SELECT load_mw FROM staging.aeso_load_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 6)), 10360)
	wantNull(t, "future load", floatAt(t, l, `SELECT load_mw FROM staging.aeso_load_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 8)))
	wantFloat(t, "forecast load", floatAt(t, l, `SELECT forecast_load_mw FROM staging.aeso_load_hourly WHERE interval_start_utc = $1`, utc(2026, 11, 6, 8)), 10174)
}

func TestLoadECCC(t *testing.T) {
	ctx := context.Background()
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{ecccKey: fixture(t, "eccc", "testdata", "climate_hourly.json")}}
	res, err := l.Load(ctx, "eccc", ecccKey, false)
	if err != nil || res.Rows != 3 {
		t.Fatalf("load = %+v, %v", res, err)
	}
	const q = `SELECT %s FROM staging.eccc_weather_hourly WHERE climate_id = '7025251' AND time_utc = $1`
	at := func(col string, h int) string { return strings.Replace(q, "%s", col, 1) + "" + strings.Repeat("", h) }
	wantFloat(t, "temp", floatAt(t, l, at("temp", 0), utc(2026, 11, 6, 0)), 4.2)
	wantNull(t, "missing temp (not zero)", floatAt(t, l, at("temp", 0), utc(2026, 11, 6, 2)))
	wantNull(t, "missing precipitation", floatAt(t, l, at("precip_amount", 0), utc(2026, 11, 6, 0)))
	wantFloat(t, "precipitation", floatAt(t, l, at("precip_amount", 0), utc(2026, 11, 6, 1)), 0.2)
	if f := textAt(t, l, at("temp_flag", 0), utc(2026, 11, 6, 2)); f == nil || *f != "M" {
		t.Errorf("temp flag = %v, want M", f)
	}
	if f := textAt(t, l, at("temp_flag", 0), utc(2026, 11, 6, 0)); f != nil {
		t.Errorf("temp flag = %q, want NULL", *f)
	}
}

func TestLoadAllSourcesAndRerun(t *testing.T) {
	ctx := context.Background()
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{
		genKey:    fixture(t, "ieso", "testdata", "GenOutputCapability.xml"),
		demandKey: fixture(t, "ieso", "testdata", "Demand.csv"),
		priceKey:  fixture(t, "ieso", "testdata", "DAHourlyOntarioZonalPrice.xml"),
		poolKey:   fixture(t, "aeso", "testdata", "PoolPrice.json"),
		aesoLoad:  fixture(t, "aeso", "testdata", "AlbertaInternalLoad.json"),
		ecccKey:   fixture(t, "eccc", "testdata", "climate_hourly.json"),
	}}
	for _, name := range SourceNames() {
		if name == "hydroquebec" {
			continue // covered by its own tests
		}
		results, err := l.LoadAll(ctx, name, false)
		if err != nil || len(results) != 1 || results[0].Skipped {
			t.Fatalf("%s first run = %+v, %v", name, results, err)
		}
		results, err = l.LoadAll(ctx, name, false)
		if err != nil || len(results) != 1 || !results[0].Skipped {
			t.Fatalf("%s second run = %+v, %v, want skipped", name, results, err)
		}
	}
	if n := countRows(t, l, "staging.load_log"); n != 6 {
		t.Errorf("load_log rows = %d, want 6", n)
	}
}

func TestUnknownSource(t *testing.T) {
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{}}
	if _, err := l.LoadAll(context.Background(), "nope", false); err == nil {
		t.Error("expected an error for an unknown source")
	}
	if len(SourceNames()) != 7 {
		t.Errorf("sources = %v, want 7", SourceNames())
	}
}
