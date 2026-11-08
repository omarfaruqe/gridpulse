package hydroquebec

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
	"github.com/omarfaruqe/gridpulse/internal/timeutil"
)

func loadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/demand.json")
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

func TestParse(t *testing.T) {
	obs, err := Parse(loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 5 {
		t.Fatalf("len = %d, want 5", len(obs))
	}
	// 2026-11-06 is after the fall-back change, so Eastern is UTC-5.
	if want := time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC); !obs[0].TimeUTC.Equal(want) {
		t.Errorf("TimeUTC = %v, want %v", obs[0].TimeUTC, want)
	}
	if obs[0].DemandMW == nil || *obs[0].DemandMW != 16172 {
		t.Errorf("first demand = %v, want 16172", obs[0].DemandMW)
	}
	if obs[3].DemandMW != nil {
		t.Errorf("empty slot should be nil, got %v", *obs[3].DemandMW)
	}
	if obs[0].LocalTime != "2026-11-06T00:00:00" {
		t.Errorf("raw local time not preserved: %q", obs[0].LocalTime)
	}
}

func TestParseRejectsDrift(t *testing.T) {
	for name, in := range map[string]string{
		"not json":   `nope`,
		"no details": `{"dateStart":"x"}`,
		"bad date":   `{"details":[{"date":"yesterday","valeurs":{}}]}`,
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRunStoresRawAndIsIdempotent(t *testing.T) {
	fixture := loadFixture(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "try later", http.StatusServiceUnavailable) // exercises retry
			return
		}
		w.Header().Set("Last-Modified", "Sat, 07 Nov 2026 22:31:33 GMT")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	store := &fakeStore{objects: map[string][]byte{}}
	ex := &Extractor{URL: srv.URL, Client: srv.Client(), Store: store, Attempts: 3, BaseDelay: time.Millisecond}

	res, err := ex.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "source=hydroquebec_demand/date=2026-11-07/demand_20261107T223133Z.json"
	if res.Key != want {
		t.Errorf("key = %q, want %q", res.Key, want)
	}
	if res.Records != 5 || res.Filled != 3 {
		t.Errorf("records/filled = %d/%d, want 5/3", res.Records, res.Filled)
	}
	if !bytes.Equal(store.objects[want], fixture) {
		t.Error("stored bytes differ from upstream (bronze must be raw)")
	}

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.objects) != 1 {
		t.Errorf("objects = %d after second run, want 1", len(store.objects))
	}
}

func TestRunFailsAfterRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := &Extractor{URL: srv.URL, Client: srv.Client(), Store: store, Attempts: 2, BaseDelay: time.Millisecond}
	if _, err := ex.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if len(store.objects) != 0 {
		t.Error("nothing should be stored on failure")
	}
}

func TestParseFlagsDaylightSavingEdges(t *testing.T) {
	feed := `{"details":[
		{"date":"2026-11-01T00:30:00","valeurs":{"demandeTotal":1}},
		{"date":"2026-11-01T01:30:00","valeurs":{"demandeTotal":2}},
		{"date":"2026-11-01T02:30:00","valeurs":{"demandeTotal":3}},
		{"date":"2026-03-08T02:30:00","valeurs":{"demandeTotal":4}}]}`
	obs, err := Parse([]byte(feed))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		utc time.Time
		res timeutil.Resolution
	}{
		{time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC), timeutil.Unique},
		{time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), timeutil.Ambiguous},
		{time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC), timeutil.Unique},
		{time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), timeutil.Nonexistent},
	}
	for i, w := range want {
		if !obs[i].TimeUTC.Equal(w.utc) || obs[i].Resolution != w.res {
			t.Errorf("obs[%d] = %v (%v), want %v (%v)", i, obs[i].TimeUTC, obs[i].Resolution, w.utc, w.res)
		}
	}
}
