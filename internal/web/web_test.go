package web

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/api"
)

func newWeb(t *testing.T) http.Handler {
	t.Helper()
	w, err := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { _, _ = rw.Write([]byte("api:" + r.URL.Path)) })
	return w.Handler(next)
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestLanguagesHaveSameKeys(t *testing.T) {
	for k := range text["en"] {
		if _, ok := text["fr"][k]; !ok {
			t.Errorf("key %q has no French text", k)
		}
	}
	for k := range text["fr"] {
		if _, ok := text["en"][k]; !ok {
			t.Errorf("key %q has no English text", k)
		}
	}
}

func TestDashboardRendersInBothLanguages(t *testing.T) {
	h := newWeb(t)
	for lang, want := range map[string]string{"en": "Electricity demand", "fr": "Demande d&#39;électricité"} {
		rec := get(h, "/?p=AB&days=30&lang="+lang)
		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("lang %s: status %d, missing %q", lang, rec.Code, want)
		}
		if !strings.Contains(body, "province=AB") || !strings.Contains(body, "/api/v1/prices?") {
			t.Errorf("lang %s: Alberta page should chart load and price", lang)
		}
		if strings.Contains(body, "data-chart=\"generation\"") {
			t.Errorf("lang %s: generation by fuel is Ontario only", lang)
		}
	}
	if !strings.Contains(get(h, "/?p=QC").Body.String(), "Quebec has no hourly market price") {
		t.Error("Quebec page should explain the missing price series")
	}
	if !strings.Contains(get(h, "/?p=ON").Body.String(), "data-chart=\"generation\"") {
		t.Error("Ontario page should chart generation")
	}
}

func TestUnknownProvinceFallsBackAndIsNotEchoed(t *testing.T) {
	rec := get(newWeb(t), "/?p=%3Cscript%3Ealert(1)%3C/script%3E&days=999")
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "alert(1)") || !strings.Contains(body, "province=ON") {
		t.Fatalf("unexpected response: %d", rec.Code)
	}
}

func TestSecurityHeadersAndFallThrough(t *testing.T) {
	h := newWeb(t)
	rec := get(h, "/")
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Error("missing content security policy")
	}
	if rec := get(h, "/api/v1/health"); rec.Body.String() != "api:/api/v1/health" {
		t.Errorf("API paths must reach the API, got %q", rec.Body.String())
	}
	if rec := get(h, "/static/style.css"); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("stylesheet: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, f := range []string{"htmx.min.js", "chart.umd.min.js", "app.js"} {
		if rec := get(h, "/static/"+f); rec.Code != 200 {
			t.Errorf("/static/%s answered %d (download it into internal/web/static)", f, rec.Code)
		}
	}
}

func TestPipelineBodyRendersAllStates(t *testing.T) {
	w, err := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	latest := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	lag, finished, errText := int64(-34114), "2026-11-08T16:12:00Z", "boom <b>"
	for _, lang := range []string{"en", "fr"} {
		view := pipelineView{base: base{Lang: lang, T: text[lang]}, H: api.PipelineHealth{
			GeneratedAt: latest,
			Summary:     api.PipelineSummary{StaleSeries: 1, FailingJobs: 1, FailingTests: 1},
			Freshness: []api.FreshnessEntry{
				{Series: "load_QC", LatestHourUTC: &latest, LagSeconds: &lag, MaxLagSeconds: 10800, Stale: true},
				{Series: "never", MaxLagSeconds: 3600},
			},
			Jobs:    []api.JobStatus{{Job: "eccc", LastStatus: "failed", LastStartedAt: finished, LastFinishedAt: &finished, LastError: &errText, Runs24h: 2, Failures24h: 1}},
			Quality: []api.QualityResult{{Test: "t", Violations: 2, CheckedAt: finished}},
		}}
		var buf bytes.Buffer
		if err := w.pages["pipeline"].ExecuteTemplate(&buf, "pipeline_body", view); err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		out := buf.String()
		if !strings.Contains(out, "boom &lt;b&gt;") || strings.Contains(out, "boom <b>") {
			t.Errorf("%s: job error must be escaped", lang)
		}
		if !strings.Contains(out, "2026-11-08 16:12 UTC") || !strings.Contains(out, "−9 h 28 min") {
			t.Errorf("%s: timestamps or lag not formatted:\n%s", lang, out)
		}
	}
}

func TestDur(t *testing.T) {
	for in, want := range map[int64]string{300: "5 min", 3900: "1 h 05 min", 200000: "2 d 7 h", -34114: "−9 h 28 min"} {
		if got := dur(in); got != want {
			t.Errorf("dur(%d) = %q, want %q", in, got, want)
		}
	}
}
