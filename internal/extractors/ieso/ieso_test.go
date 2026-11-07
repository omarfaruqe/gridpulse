package ieso

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/GenOutputCapability.xml")
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

func TestParseGenOutputCapability(t *testing.T) {
	rows, err := ParseGenOutputCapability(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(rows))
	}
	first := rows[0]
	if first.Generator != "BRUCEA-G1" || first.Fuel != "NUCLEAR" || first.HourEnding != 1 {
		t.Errorf("first row = %+v", first)
	}
	// Hour 1 of 2026-11-06 starts 00:00 EST (UTC-5) = 05:00 UTC.
	if want := time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC); !first.IntervalStartUTC.Equal(want) {
		t.Errorf("IntervalStartUTC = %v, want %v", first.IntervalStartUTC, want)
	}
	if first.EnergyMW == nil || *first.EnergyMW != 770 {
		t.Errorf("EnergyMW = %v, want 770", first.EnergyMW)
	}
	if rows[4].EnergyMW != nil {
		t.Errorf("hour with no EnergyMW should be nil, got %v", *rows[4].EnergyMW)
	}
}

func TestParseRejectsDrift(t *testing.T) {
	for name, in := range map[string]string{
		"not xml":       `nope`,
		"no generators": `<IMODocument><IMODocBody><Date>2026-11-06</Date></IMODocBody></IMODocument>`,
		"bad date":      `<IMODocument><IMODocBody><Date>x</Date><Generators><Generator/></Generators></IMODocBody></IMODocument>`,
		"bad hour": `<IMODocument><IMODocBody><Date>2026-11-06</Date><Generators><Generator><GeneratorName>A</GeneratorName>` +
			`<Outputs><Output><Hour>25</Hour></Output></Outputs></Generator></Generators></IMODocBody></IMODocument>`,
	} {
		if _, err := ParseGenOutputCapability([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func newTestExtractor(srv *httptest.Server, store *fakeStore) *Extractor {
	return &Extractor{BaseURL: srv.URL, Client: srv.Client(), Store: store, Attempts: 2, BaseDelay: time.Millisecond}
}

func TestFetchDayAndBackfillAreIdempotent(t *testing.T) {
	data := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/GenOutputCapability/PUB_GenOutputCapability_20261106.xml",
			"/GenOutputCapability/PUB_GenOutputCapability_20261107.xml":
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)
	from := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC) // Nov 8 is not published

	for i := 0; i < 2; i++ { // second pass must not create new objects
		stored, missing, err := ex.Backfill(context.Background(), "GenOutputCapability", from, to)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) != 2 || len(missing) != 1 || missing[0] != "2026-11-08" {
			t.Fatalf("pass %d: stored=%d missing=%v", i, len(stored), missing)
		}
	}
	if len(store.objects) != 2 {
		t.Errorf("objects = %d, want 2", len(store.objects))
	}
	key := "source=ieso_genoutputcapability/date=2026-11-06/PUB_GenOutputCapability_20261106.xml"
	if !bytes.Equal(store.objects[key], data) {
		t.Errorf("object %q missing or not raw", key)
	}
}

func TestFetchDayNotPublishedIsNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer srv.Close()
	ex := newTestExtractor(srv, &fakeStore{objects: map[string][]byte{}})
	_, err := ex.FetchDay(context.Background(), "GenOutputCapability", time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrNotPublished) || calls != 1 {
		t.Fatalf("err = %v, calls = %d, want ErrNotPublished and 1 call", err, calls)
	}
}

func TestFetchDayRejectsInvalidReportAndUnknownName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<broken`))
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	if _, err := ex.FetchDay(context.Background(), "GenOutputCapability", day); err == nil {
		t.Error("malformed XML should fail validation")
	}
	if len(store.objects) != 0 {
		t.Error("nothing should be stored for an invalid report")
	}
	if _, err := ex.FetchDay(context.Background(), "NoSuchReport", day); err == nil {
		t.Error("unsupported report should fail")
	}
}
