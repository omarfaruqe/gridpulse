package eccc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
)

type fakeStore struct{ objects map[string][]byte }

func (f *fakeStore) Put(_ context.Context, source string, date time.Time, filename, _ string, data []byte) (string, error) {
	key, err := bronze.Key(source, date, filename)
	if err != nil {
		return "", err
	}
	f.objects[key] = data
	return key, nil
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/climate_hourly.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// collection builds an API response with `hours` hourly rows for one station and UTC day.
func collection(cid string, y, m, d, hours int) []byte {
	rows := make([]string, 0, hours)
	for h := 0; h < hours; h++ {
		rows = append(rows, fmt.Sprintf(`{"type":"Feature","properties":{"STATION_NAME":"STATION %s","CLIMATE_IDENTIFIER":"%s","PROVINCE_CODE":"QC","UTC_DATE":"%04d-%02d-%02dT%02d:00:00","TEMP":%d.5,"TEMP_FLAG":null,"RELATIVE_HUMIDITY":80,"WIND_SPEED":10}}`, cid, cid, y, m, d, h, h))
	}
	return []byte(fmt.Sprintf(`{"type":"FeatureCollection","numberMatched":%d,"numberReturned":%d,"features":[%s]}`, hours, hours, strings.Join(rows, ",")))
}

// handler serves generated data and counts calls. hours(cid) sets how many rows each station has.
func handler(calls *atomic.Int32, hours func(cid string) int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/collections/climate-hourly/items" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		y, _ := strconv.Atoi(q.Get("UTC_YEAR"))
		m, _ := strconv.Atoi(q.Get("UTC_MONTH"))
		d, _ := strconv.Atoi(q.Get("UTC_DAY"))
		cid := q.Get("CLIMATE_IDENTIFIER")
		_, _ = w.Write(collection(cid, y, m, d, hours(cid)))
	}
}

func newTestExtractor(t *testing.T, srv *httptest.Server, store *fakeStore) *Extractor {
	t.Helper()
	stations, err := Stations()
	if err != nil {
		t.Fatal(err)
	}
	return &Extractor{BaseURL: srv.URL, Client: srv.Client(), Store: store, Stations: stations, Attempts: 3, BaseDelay: time.Millisecond}
}

var montreal = Station{ClimateID: "7025251", Name: "MONTREAL INTL A", Province: "QC", Region: "QC", Operator: "hydroquebec", Weight: 1}

func TestStationsLoad(t *testing.T) {
	stations, err := Stations()
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != 6 {
		t.Fatalf("stations = %d, want 6", len(stations))
	}
	operators := map[string]int{}
	for _, s := range stations {
		operators[s.Operator]++
	}
	if operators["hydroquebec"] != 2 || operators["ieso"] != 2 || operators["aeso"] != 2 {
		t.Errorf("operators = %v, want 2 each", operators)
	}
}

func TestValidateStations(t *testing.T) {
	ok := Station{ClimateID: "1", Name: "A", Province: "QC", Region: "QC", Operator: "x", Weight: 1}
	tests := map[string][]Station{
		"empty":          {},
		"duplicate id":   {ok, ok},
		"missing field":  {{ClimateID: "1", Weight: 1}},
		"weights not 1":  {{ClimateID: "1", Name: "A", Province: "QC", Region: "QC", Operator: "x", Weight: 0.5}},
		"zero weight":    {{ClimateID: "1", Name: "A", Province: "QC", Region: "QC", Operator: "x", Weight: 0}},
		"weights over 1": {{ClimateID: "1", Name: "A", Province: "QC", Region: "QC", Operator: "x", Weight: 0.7}, {ClimateID: "2", Name: "B", Province: "QC", Region: "QC", Operator: "x", Weight: 0.7}},
	}
	for name, in := range tests {
		if err := validateStations(in); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := validateStations([]Station{ok}); err != nil {
		t.Errorf("valid station rejected: %v", err)
	}
}

func TestParseHourly(t *testing.T) {
	obs, err := ParseHourly(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 3 {
		t.Fatalf("rows = %d, want 3", len(obs))
	}
	if want := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC); !obs[0].TimeUTC.Equal(want) {
		t.Errorf("TimeUTC = %v, want %v", obs[0].TimeUTC, want)
	}
	if obs[0].Temp == nil || *obs[0].Temp != 4.2 || obs[0].ClimateID != "7025251" {
		t.Errorf("first row = %+v", obs[0])
	}
	if obs[0].PrecipAmount != nil {
		t.Error("null precipitation must stay nil, not 0")
	}
	if obs[2].Temp != nil || obs[2].RelativeHumidity != nil {
		t.Error("null temperature and humidity must stay nil")
	}
	if obs[2].TempFlag != "M" || obs[2].RelHumidityFlag != "M" || obs[0].TempFlag != "" {
		t.Errorf("flags = %q/%q/%q", obs[2].TempFlag, obs[2].RelHumidityFlag, obs[0].TempFlag)
	}
	if obs[2].WindSpeed == nil || *obs[2].WindSpeed != 11 {
		t.Errorf("WindSpeed = %v, want 11", obs[2].WindSpeed)
	}
}

func TestParseHourlyRejectsDrift(t *testing.T) {
	row := func(extra string) string {
		return `{"type":"FeatureCollection","numberMatched":1,"numberReturned":1,"features":[{"properties":{"CLIMATE_IDENTIFIER":"1",` + extra + `}}]}`
	}
	for name, in := range map[string]string{
		"not json":   `nope`,
		"wrong type": `{"type":"Feature","features":[]}`,
		"truncated":  `{"type":"FeatureCollection","numberMatched":5,"numberReturned":1,"features":[{"properties":{"CLIMATE_IDENTIFIER":"1","UTC_DATE":"2026-11-06T00:00:00"}}]}`,
		"bad date":   row(`"UTC_DATE":"yesterday"`),
		"text temp":  row(`"UTC_DATE":"2026-11-06T00:00:00","TEMP":"warm"`),
		"duplicate hour": `{"type":"FeatureCollection","numberMatched":2,"numberReturned":2,"features":[` +
			`{"properties":{"CLIMATE_IDENTIFIER":"1","UTC_DATE":"2026-11-06T00:00:00"}},` +
			`{"properties":{"CLIMATE_IDENTIFIER":"1","UTC_DATE":"2026-11-06T00:00:00"}}]}`,
	} {
		if _, err := ParseHourly([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestFetchStationDayStoresRawAndIsIdempotent(t *testing.T) {
	data := fixture(t)
	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(map[string][]string(r.URL.Query()))
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(t, srv, store)
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ { // second run must not create a second object
		res, err := ex.FetchStationDay(context.Background(), montreal, day)
		if err != nil {
			t.Fatal(err)
		}
		if res.Rows != 3 || res.Day != "2026-11-06" || res.ClimateID != "7025251" {
			t.Errorf("result = %+v", res)
		}
	}
	q := gotQuery.Load().(map[string][]string)
	if q["CLIMATE_IDENTIFIER"][0] != "7025251" || q["UTC_YEAR"][0] != "2026" || q["UTC_MONTH"][0] != "11" || q["UTC_DAY"][0] != "6" {
		t.Errorf("query = %v", q)
	}
	key := "source=eccc_climate_hourly/date=2026-11-06/7025251_2026-11-06.json"
	if len(store.objects) != 1 || !bytes.Equal(store.objects[key], data) {
		t.Errorf("objects = %d, want 1 raw object at %q", len(store.objects), key)
	}
}

func TestFetchStationDayNoDataStoresNothing(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(handler(&calls, func(string) int { return 0 }))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(t, srv, store)
	_, err := ex.FetchStationDay(context.Background(), montreal, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrNoData) || len(store.objects) != 0 {
		t.Fatalf("err = %v, objects = %d, want ErrNoData and 0", err, len(store.objects))
	}
}

func TestFetchStationDayRejectsMismatchedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(collection("9999999", 2026, 11, 6, 24)) // wrong station
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(t, srv, store)
	if _, err := ex.FetchStationDay(context.Background(), montreal, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("wrong station should fail")
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(collection("7025251", 2026, 11, 5, 24)) // wrong day
	}))
	defer srv2.Close()
	ex = newTestExtractor(t, srv2, store)
	if _, err := ex.FetchStationDay(context.Background(), montreal, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("wrong day should fail")
	}
	if len(store.objects) != 0 {
		t.Error("nothing should be stored for a mismatched response")
	}
}

func TestFetchStationDayRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(collection("7025251", 2026, 11, 6, 24))
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(t, srv, store)
	if _, err := ex.FetchStationDay(context.Background(), montreal, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(store.objects) != 1 {
		t.Errorf("calls = %d, objects = %d, want 2 and 1", calls.Load(), len(store.objects))
	}
}

func TestFetchStationDayClientErrorIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	ex := newTestExtractor(t, srv, &fakeStore{objects: map[string][]byte{}})
	if _, err := ex.FetchStationDay(context.Background(), montreal, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err == nil || calls.Load() != 1 {
		t.Fatalf("err = %v, calls = %d, want an error after 1 call", err, calls.Load())
	}
}

func TestFetchDayReportsMissingStations(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(handler(&calls, func(cid string) int {
		if cid == "7025251" {
			return 24
		}
		return 0 // other stations have not published yet
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(t, srv, store)
	stored, missing, err := ex.FetchDay(context.Background(), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || len(missing) != 5 || len(store.objects) != 1 {
		t.Errorf("stored=%d missing=%d objects=%d, want 1/5/1", len(stored), len(missing), len(store.objects))
	}
}

func TestFetchDayOneFailureDoesNotStopTheRest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("CLIMATE_IDENTIFIER") == "7016293" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		y, _ := strconv.Atoi(q.Get("UTC_YEAR"))
		m, _ := strconv.Atoi(q.Get("UTC_MONTH"))
		d, _ := strconv.Atoi(q.Get("UTC_DAY"))
		_, _ = w.Write(collection(q.Get("CLIMATE_IDENTIFIER"), y, m, d, 24))
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(t, srv, store)
	stored, _, err := ex.FetchDay(context.Background(), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "7016293") {
		t.Errorf("err = %v, want an error naming station 7016293", err)
	}
	if len(stored) != 5 {
		t.Errorf("stored = %d, want the other 5 stations", len(stored))
	}
}

func TestBackfillIsIdempotent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(handler(&calls, func(string) int { return 24 }))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(t, srv, store)
	from := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ { // re-running a backfill must not duplicate
		stored, missing, err := ex.Backfill(context.Background(), from, to)
		if err != nil || len(stored) != 12 || len(missing) != 0 {
			t.Fatalf("pass %d: stored=%d missing=%v err=%v", i, len(stored), missing, err)
		}
	}
	if len(store.objects) != 12 {
		t.Errorf("objects = %d, want 12 (6 stations x 2 days)", len(store.objects))
	}
	if _, _, err := ex.Backfill(context.Background(), to, from); err == nil {
		t.Error("reversed range should fail")
	}
}
