//go:build integration

package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/db/dbtest"
	"github.com/omarfaruqe/gridpulse/internal/transform"
	sqlfiles "github.com/omarfaruqe/gridpulse/sql"
)

var fixedNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func exec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// newServer builds the gold tables (empty), adds a few known rows, and returns the router.
func newServer(t *testing.T) http.Handler {
	t.Helper()
	pool := dbtest.New(t)
	if err := (&transform.Runner{Pool: pool, Files: sqlfiles.FS}).Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec(t, pool, `INSERT INTO gold.fact_hourly_load (province_code, hour_utc, load_mw, is_complete, source) VALUES
	 ('ON', '2026-11-07 00:00+00', 15000, true,  'ieso_demand'),
	 ('ON', '2026-11-07 01:00+00', NULL,  false, 'ieso_demand'),
	 ('ON', '2026-11-07 02:00+00', 14800, true,  'ieso_demand'),
	 ('ON', '2026-10-01 00:00+00', 12000, true,  'ieso_demand'),
	 ('QC', '2026-11-07 00:00+00', 30000, true,  'hydroquebec_demand')`)
	exec(t, pool, `INSERT INTO gold.fact_generation_by_fuel (province_code, hour_utc, fuel_type, generation_mw, output_quality, is_complete) VALUES
	 ('ON', '2026-11-07 00:00+00', 'NUCLEAR', 9000, 0, true),
	 ('ON', '2026-11-07 00:00+00', 'GAS',     NULL, 3, false)`)
	exec(t, pool, `INSERT INTO gold.fact_hourly_price (province_code, hour_utc, price_cad_mwh, price_kind, source)
	 VALUES ('AB', '2026-11-07 00:00+00', 55.5, 'pool_price', 'aeso_poolprice')`)
	exec(t, pool, `INSERT INTO gold.fact_hourly_weather (province_code, hour_utc, temp_c, wind_speed_kmh, stations_reporting, stations_total, is_complete)
	 VALUES ('QC', '2026-11-07 00:00+00', -3.5, NULL, 1, 2, false)`)
	exec(t, pool, `INSERT INTO gold.mart_load_weather (province_code, hour_utc, load_mw, temp_c, wind_speed_kmh, stations_reporting, stations_total)
	 VALUES ('QC', '2026-11-07 00:00+00', 30000, -3.5, 12, 2, 2)`)
	exec(t, pool, `INSERT INTO gold.recon_generation_monthly (ref_month, comparison_group, ieso_mwh, statcan_mwh, ratio, ieso_coverage, baseline_ratio, baseline_months, deviation, tolerance, status) VALUES
	 ('2026-07-01', 'Nuclear', 100, 101, 0.99, 1, 0.99, 12, 0.0, 0.02, 'ok'),
	 ('2026-08-01', 'Nuclear', 90,  101, 0.89, 1, 0.99, 12, -0.1, 0.02, 'out_of_band'),
	 ('2026-08-01', 'Hydro',   95,  100, 0.95, 1, 0.95, 12, 0.0, 0.06, 'ok')`)
	exec(t, pool, `INSERT INTO ops.job_run (job, started_at, finished_at, status, error) VALUES
	 ('hydroquebec', '2026-11-08 09:00+00', '2026-11-08 09:00:05+00', 'failed',    'upstream returned 500'),
	 ('hydroquebec', '2026-11-08 11:00+00', '2026-11-08 11:00:05+00', 'succeeded', NULL),
	 ('eccc',        '2026-11-08 10:00+00', '2026-11-08 10:01+00',    'failed',    'station 7025251: timeout'),
	 ('eccc',        '2026-11-06 10:00+00', '2026-11-06 10:01+00',    'succeeded', NULL)`)
	exec(t, pool, `INSERT INTO ops.test_result (run_at, test, violations) VALUES
	 ('2026-11-08 08:00+00', 'no_negative_load', 3),
	 ('2026-11-08 11:30+00', 'no_negative_load', 0),
	 ('2026-11-08 11:30+00', 'no_missing_hours_load', 2)`)

	exec(t, pool, `INSERT INTO ops.freshness_lag (checked_at, series, latest_hour, lag_seconds, max_lag_seconds, stale) VALUES
	 ('2026-11-08 06:00+00', 'load_QC',  '2026-11-08 05:00+00', 3600, 10800, false),
	 ('2026-11-08 09:00+00', 'load_QC',  '2026-11-08 05:00+00', 14400, 10800, true),
	 ('2026-11-08 11:00+00', 'load_QC',  '2026-11-08 10:00+00', 3600, 10800, false),
	 ('2026-10-30 09:00+00', 'load_QC',  '2026-10-29 00:00+00', 999999, 10800, true),
	 ('2026-11-08 06:00+00', 'price_ON', '2026-11-09 04:00+00', -79200, 43200, false),
	 ('2026-11-08 07:00+00', 'price_ON', '2026-11-09 04:00+00', -75600, 43200, false)`)

	a := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.Now = func() time.Time { return fixedNow }
	return a.Handler()
}

type reply struct {
	Data []map[string]any `json:"data"`
	Meta map[string]any   `json:"meta"`
}

func get(t *testing.T, h http.Handler, method, path string) (*httptest.ResponseRecorder, reply) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var r reply
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	return rec, r
}

func TestAPIDataEndpoints(t *testing.T) {
	h := newServer(t)

	t.Run("provinces", func(t *testing.T) {
		rec, r := get(t, h, "GET", "/api/v1/provinces")
		if rec.Code != 200 || len(r.Data) != 3 || r.Data[0]["code"] != "AB" || r.Data[2]["name_fr"] != "Québec" {
			t.Errorf("%d %v", rec.Code, r.Data)
		}
	})
	t.Run("fuels", func(t *testing.T) {
		_, r := get(t, h, "GET", "/api/v1/fuels")
		if len(r.Data) != 8 {
			t.Errorf("fuels = %d, want 8", len(r.Data))
		}
	})
	t.Run("load defaults to the last 7 days and keeps missing values null", func(t *testing.T) {
		rec, r := get(t, h, "GET", "/api/v1/load?province=ON")
		if rec.Code != 200 || len(r.Data) != 3 {
			t.Fatalf("%d rows = %d, want 3 (the October row is outside the default range)", rec.Code, len(r.Data))
		}
		if r.Data[0]["load_mw"] != 15000.0 || r.Data[1]["load_mw"] != nil || r.Data[1]["is_complete"] != false {
			t.Errorf("rows = %v, want a JSON null (not 0) for the missing hour", r.Data)
		}
		if r.Data[0]["hour_utc"] != "2026-11-07T00:00:00Z" {
			t.Errorf("hour_utc = %v, want an RFC 3339 UTC timestamp", r.Data[0]["hour_utc"])
		}
		if r.Meta["rows"] != 3.0 || r.Meta["from"] != "2026-11-02" || r.Meta["to"] != "2026-11-08" || r.Meta["timezone"] != "UTC" {
			t.Errorf("meta = %v", r.Meta)
		}
		if rec.Header().Get("Cache-Control") != "public, max-age=60" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("headers = %v", rec.Header())
		}
	})
	t.Run("load with an explicit range", func(t *testing.T) {
		_, r := get(t, h, "GET", "/api/v1/load?province=ON&from=2026-10-01&to=2026-10-01")
		if len(r.Data) != 1 || r.Data[0]["load_mw"] != 12000.0 {
			t.Errorf("rows = %v", r.Data)
		}
	})
	t.Run("an empty result is [] and not null", func(t *testing.T) {
		rec, _ := get(t, h, "GET", "/api/v1/load?province=AB")
		if !strings.Contains(rec.Body.String(), `"data":[]`) {
			t.Errorf("body = %s", rec.Body.String())
		}
	})
	t.Run("generation", func(t *testing.T) {
		_, r := get(t, h, "GET", "/api/v1/generation?province=ON")
		if len(r.Data) != 2 || r.Data[0]["fuel_type"] != "GAS" || r.Data[0]["generation_mw"] != nil || r.Data[0]["output_quality"] != 3.0 {
			t.Errorf("rows = %v", r.Data)
		}
		_, r = get(t, h, "GET", "/api/v1/generation?province=QC")
		if len(r.Data) != 0 || r.Meta["note"] == nil {
			t.Errorf("QC = %v %v, want no rows and a note", r.Data, r.Meta)
		}
	})
	t.Run("prices, weather and load-weather", func(t *testing.T) {
		_, r := get(t, h, "GET", "/api/v1/prices?province=AB")
		if len(r.Data) != 1 || r.Data[0]["price_cad_mwh"] != 55.5 || r.Data[0]["price_kind"] != "pool_price" {
			t.Errorf("prices = %v", r.Data)
		}
		_, r = get(t, h, "GET", "/api/v1/weather?province=QC")
		if len(r.Data) != 1 || r.Data[0]["temp_c"] != -3.5 || r.Data[0]["wind_speed_kmh"] != nil || r.Data[0]["stations_reporting"] != 1.0 {
			t.Errorf("weather = %v", r.Data)
		}
		_, r = get(t, h, "GET", "/api/v1/load-weather?province=QC")
		if len(r.Data) != 1 || r.Data[0]["load_mw"] != 30000.0 {
			t.Errorf("load-weather = %v", r.Data)
		}
	})
	t.Run("reconciliation and its filters", func(t *testing.T) {
		_, r := get(t, h, "GET", "/api/v1/reconciliation")
		if len(r.Data) != 3 || r.Data[0]["month"] != "2026-07" {
			t.Errorf("all = %v", r.Data)
		}
		_, r = get(t, h, "GET", "/api/v1/reconciliation?group=Nuclear")
		if len(r.Data) != 2 {
			t.Errorf("group = %v", r.Data)
		}
		_, r = get(t, h, "GET", "/api/v1/reconciliation?status=out_of_band")
		if len(r.Data) != 1 || r.Data[0]["deviation"] != -0.1 {
			t.Errorf("status = %v", r.Data)
		}
	})
}

func TestAPIRejectsBadRequests(t *testing.T) {
	h := newServer(t)
	for path, want := range map[string]string{
		"/api/v1/load":             "province is required",
		"/api/v1/load?province=XX": "not valid",
		"/api/v1/load?province=ON%27%3BDROP%20TABLE%20x%3B--":    "not valid",
		"/api/v1/load?province=ON&from=2026-11-05&to=2026-11-01": "from is after to",
		"/api/v1/load?province=ON&from=2025-01-01&to=2026-01-01": "maximum",
		"/api/v1/load?province=ON&from=last-week":                "not a date",
		"/api/v1/reconciliation?group=Bogus":                     "not valid",
		"/api/v1/reconciliation?status=Bogus":                    "not valid",
	} {
		rec, _ := get(t, h, "GET", path)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s = %d %s, want 400 containing %q", path, rec.Code, rec.Body.String(), want)
		}
	}
	if rec, _ := get(t, h, "GET", "/api/v1/nope"); rec.Code != 404 || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("unknown path = %d %s, want a JSON 404", rec.Code, rec.Body.String())
	}
	if rec, _ := get(t, h, "POST", "/api/v1/load?province=ON"); rec.Code != 405 {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
}

func TestAPIHealthAndPipeline(t *testing.T) {
	h := newServer(t)

	if rec, _ := get(t, h, "GET", "/api/v1/health"); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("health = %d %v", rec.Code, rec.Header())
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/pipeline", nil))
	if rec.Code != 200 {
		t.Fatalf("pipeline = %d %s", rec.Code, rec.Body.String())
	}
	var p PipelineHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if !p.GeneratedAt.Equal(fixedNow) {
		t.Errorf("generated_at = %v", p.GeneratedAt)
	}
	if len(p.Freshness) != 9 {
		t.Errorf("freshness series = %d, want 9", len(p.Freshness))
	}
	worst := map[string]*int64{}
	for _, f := range p.Freshness {
		worst[f.Series] = f.WorstLag7dSeconds
	}
	if w := worst["load_QC"]; w == nil || *w != 14400 {
		t.Errorf("load_QC worst lag = %v, want 14400 (the check 9 days ago is outside the window)", w)
	}
	if w := worst["price_ON"]; w == nil || *w != -75600 {
		t.Errorf("price_ON worst lag = %v, want -75600 (the largest of the negative lags)", w)
	}
	if worst["weather_QC"] != nil {
		t.Errorf("weather_QC worst lag = %v, want null (no checks recorded)", *worst["weather_QC"])
	}
	byJob := map[string]JobStatus{}
	for _, j := range p.Jobs {
		byJob[j.Job] = j
	}
	hq, eccc := byJob["hydroquebec"], byJob["eccc"]
	if hq.LastStatus != "succeeded" || hq.Runs24h != 2 || hq.Failures24h != 1 || hq.LastError != nil {
		t.Errorf("hydroquebec = %+v, want the latest run to count (succeeded) with one failure in 24 h", hq)
	}
	if eccc.LastStatus != "failed" || eccc.LastError == nil || eccc.Runs24h != 1 || eccc.LastSuccessAt == nil {
		t.Errorf("eccc = %+v, want failed with its error, 1 run in 24 h (the success was 2 days ago)", eccc)
	}
	byTest := map[string]QualityResult{}
	for _, q := range p.Quality {
		byTest[q.Test] = q
	}
	if byTest["no_negative_load"].Violations != 0 || byTest["no_missing_hours_load"].Violations != 2 {
		t.Errorf("quality = %+v, want only the latest result of each test", p.Quality)
	}
	if p.Summary.FailingJobs != 1 || p.Summary.FailingTests != 1 || p.Summary.StaleSeries < 1 {
		t.Errorf("summary = %+v, want 1 failing job, 1 failing test, some stale series", p.Summary)
	}
}
