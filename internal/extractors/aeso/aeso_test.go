package aeso

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fakeStore struct{ objects map[string][]byte }

func (f *fakeStore) Put(_ context.Context, source string, date time.Time, filename, _ string, data []byte) (string, error) {
	key, err := bronze.Key(source, date, filename)
	if err != nil {
		return "", err
	}
	f.objects[key] = data
	return key, nil
}

func newTestExtractor(srv *httptest.Server, store *fakeStore) *Extractor {
	return &Extractor{
		BaseURL: srv.URL, APIKey: "test-key", Client: srv.Client(), Store: store,
		Attempts: 3, BaseDelay: time.Millisecond,
	}
}

func TestParsePoolPrice(t *testing.T) {
	rows, err := ParsePoolPrice(fixture(t, "PoolPrice.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if want := time.Date(2026, 11, 6, 6, 0, 0, 0, time.UTC); !rows[0].StartUTC.Equal(want) {
		t.Errorf("StartUTC = %v, want %v", rows[0].StartUTC, want)
	}
	if rows[0].PoolPrice == nil || *rows[0].PoolPrice != 10.51 {
		t.Errorf("PoolPrice = %v, want 10.51", rows[0].PoolPrice)
	}
	if rows[2].PoolPrice != nil || rows[2].Rolling30DayAvg != nil {
		t.Error("blank values must be nil, not 0")
	}
	if rows[2].ForecastPoolPrice == nil || *rows[2].ForecastPoolPrice != 10.22 {
		t.Errorf("ForecastPoolPrice = %v, want 10.22", rows[2].ForecastPoolPrice)
	}
}

func TestParseAlbertaInternalLoad(t *testing.T) {
	rows, err := ParseAlbertaInternalLoad(fixture(t, "AlbertaInternalLoad.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].LoadMW == nil || *rows[0].LoadMW != 10360 {
		t.Errorf("LoadMW = %v, want 10360", rows[0].LoadMW)
	}
	if rows[2].LoadMW != nil {
		t.Error("blank actual load must be nil, not 0")
	}
	if rows[2].ForecastLoadMW == nil || *rows[2].ForecastLoadMW != 10174 {
		t.Errorf("ForecastLoadMW = %v, want 10174", rows[2].ForecastLoadMW)
	}
}

func TestParseRejectsDrift(t *testing.T) {
	const ok = `"responseCode":"200"`
	for name, in := range map[string]string{
		"not json":       `nope`,
		"error response": `{"responseCode":"500","return":{}}`,
		"no report key":  `{` + ok + `,"return":{"Other":[]}}`,
		"empty report":   `{` + ok + `,"return":{"Pool Price Report":[]}}`,
		"return is list": `{` + ok + `,"return":[]}`,
		"bad time":       `{` + ok + `,"return":{"Pool Price Report":[{"begin_datetime_utc":"yesterday"}]}}`,
		"bad number":     `{` + ok + `,"return":{"Pool Price Report":[{"begin_datetime_utc":"2026-11-06 06:00","pool_price":"abc"}]}}`,
		"number type":    `{` + ok + `,"return":{"Pool Price Report":[{"begin_datetime_utc":"2026-11-06 06:00","pool_price":10.5}]}}`,
	} {
		if _, err := ParsePoolPrice([]byte(in)); err == nil {
			t.Errorf("pool price %s: expected error", name)
		}
	}
	if _, err := ParseAlbertaInternalLoad([]byte(`{"return":{"Pool Price Report":[]}}`)); err == nil {
		t.Error("load: wrong report key should fail")
	}
}

func TestNewRequiresKey(t *testing.T) {
	if _, err := New(&fakeStore{}, ""); err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestFetchDayStoresRawAndIsIdempotent(t *testing.T) {
	data := fixture(t, "PoolPrice.json")
	var gotKey, gotStart, gotEnd atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey.Store(r.Header.Get("API-KEY"))
		gotStart.Store(r.URL.Query().Get("startDate"))
		gotEnd.Store(r.URL.Query().Get("endDate"))
		if r.URL.Path != "/poolprice-api/v1.1/price/poolPrice" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ { // second run must not create a second object
		res, err := ex.FetchDay(context.Background(), "PoolPrice", day)
		if err != nil {
			t.Fatal(err)
		}
		if res.Rows != 3 || res.Day != "2026-11-06" {
			t.Errorf("result = %+v", res)
		}
	}
	if gotKey.Load() != "test-key" || gotStart.Load() != "2026-11-06" || gotEnd.Load() != "2026-11-06" {
		t.Errorf("request key/start/end = %v/%v/%v", gotKey.Load(), gotStart.Load(), gotEnd.Load())
	}
	key := "source=aeso_poolprice/date=2026-11-06/PoolPrice_2026-11-06.json"
	if len(store.objects) != 1 || !bytes.Equal(store.objects[key], data) {
		t.Errorf("objects = %d, want 1 raw object at %q", len(store.objects), key)
	}
}

func TestFetchDayAuthFailureIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	ex := newTestExtractor(srv, &fakeStore{objects: map[string][]byte{}})
	_, err := ex.FetchDay(context.Background(), "PoolPrice", time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrAuth) || calls.Load() != 1 {
		t.Fatalf("err = %v, calls = %d, want ErrAuth and 1 call", err, calls.Load())
	}
}

func TestFetchDayRetriesServerErrors(t *testing.T) {
	data := fixture(t, "AlbertaInternalLoad.json")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)
	if _, err := ex.FetchDay(context.Background(), "AlbertaInternalLoad", time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(store.objects) != 1 {
		t.Errorf("calls = %d, objects = %d, want 2 and 1", calls.Load(), len(store.objects))
	}
}

func TestFetchDayStoresNothingWhenInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"responseCode":"200","return":{"Other":[]}}`))
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	if _, err := ex.FetchDay(context.Background(), "PoolPrice", day); err == nil {
		t.Error("schema drift should fail")
	}
	if len(store.objects) != 0 {
		t.Error("nothing should be stored for an invalid response")
	}
	if _, err := ex.FetchDay(context.Background(), "NoSuchReport", day); err == nil {
		t.Error("unsupported report should fail")
	}
}

func TestBackfill(t *testing.T) {
	data := fixture(t, "PoolPrice.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)
	from := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ { // re-running a backfill must not duplicate
		stored, err := ex.Backfill(context.Background(), "PoolPrice", from, to)
		if err != nil || len(stored) != 3 {
			t.Fatalf("pass %d: stored=%d err=%v", i, len(stored), err)
		}
	}
	if len(store.objects) != 3 {
		t.Errorf("objects = %d, want 3", len(store.objects))
	}
	if _, err := ex.Backfill(context.Background(), "PoolPrice", to, from); err == nil {
		t.Error("reversed range should fail")
	}
}
