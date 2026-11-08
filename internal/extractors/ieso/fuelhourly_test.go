package ieso

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fuelFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/GenOutputbyFuelHourly.xml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseGenOutputbyFuelHourly(t *testing.T) {
	rows, created, err := ParseGenOutputbyFuelHourly(fuelFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// CreatedAt 06:50:56 EST = 11:50:56 UTC.
	if want := time.Date(2026, 11, 8, 11, 50, 56, 0, time.UTC); !created.UTC().Equal(want) {
		t.Errorf("created = %v, want %v", created.UTC(), want)
	}
	if len(rows) != 12 {
		t.Fatalf("rows = %d, want 12", len(rows))
	}
	// Hour 1 of 2026-01-01 starts 00:00 EST = 05:00 UTC.
	first := rows[0]
	if first.Fuel != "NUCLEAR" || first.OutputMW == nil || *first.OutputMW != 9602 ||
		!first.IntervalStartUTC.Equal(time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("first row = %+v", first)
	}
	// Quality is kept as published, including the undocumented -1.
	if gas := rows[1]; gas.Quality == nil || *gas.Quality != -1 || *gas.OutputMW != 3246 {
		t.Errorf("gas hour 1 = %+v", gas)
	}
	// An absent Output stays nil (not zero), with its quality flag.
	var gap FuelHour
	for _, r := range rows {
		if r.Fuel == "GAS" && r.HourEnding == 2 {
			gap = r
		}
	}
	if gap.OutputMW != nil || gap.Quality == nil || *gap.Quality != 3 {
		t.Errorf("gas hour 2 = %+v, want nil output with quality 3", gap)
	}
	// Day 2 hour 1 starts 2026-01-02 00:00 EST.
	if last := rows[len(rows)-1]; !last.IntervalStartUTC.Equal(time.Date(2026, 1, 2, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("last row start = %v", last.IntervalStartUTC)
	}
}

func TestParseGenOutputbyFuelHourlyRejectsDrift(t *testing.T) {
	good := string(fuelFixture(t))
	cases := map[string]string{
		"not xml":         "hello",
		"no days":         strings.Replace(good, "<DailyData>", "<Skipped>", 1),
		"bad created":     strings.Replace(good, "2026-11-08T06:50:56", "yesterday", 1),
		"wrong year":      strings.Replace(good, "<DeliveryYear>2026", "<DeliveryYear>2025", 1),
		"hour 25":         strings.Replace(good, "<Hour>2</Hour>", "<Hour>25</Hour>", 1),
		"bad output":      strings.Replace(good, "<Output>9602</Output>", "<Output>lots</Output>", 1),
		"bad quality":     strings.Replace(good, "<OutputQuality>-1</OutputQuality>", "<OutputQuality>ok</OutputQuality>", 1),
		"duplicate total": strings.Replace(good, "<Fuel>HYDRO</Fuel>", "<Fuel>GAS</Fuel>", 1),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseGenOutputbyFuelHourly([]byte(doc)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

type recordedPut struct{ source, filename, contentType string }

type fuelPutter struct{ puts []recordedPut }

func (p *fuelPutter) Put(_ context.Context, source string, _ time.Time, filename, contentType string, _ []byte) (string, error) {
	p.puts = append(p.puts, recordedPut{source, filename, contentType})
	return source + "/" + filename, nil
}

func TestFetchYearStoresFuelReportAsXML(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write(fuelFixture(t))
	}))
	defer srv.Close()

	store := &fuelPutter{}
	ex := New(store)
	ex.BaseURL = srv.URL
	res, err := ex.FetchYear(context.Background(), "GenOutputbyFuelHourly", 2026)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/GenOutputbyFuelHourly/PUB_GenOutputbyFuelHourly_2026.xml" {
		t.Errorf("requested %s", path)
	}
	if res.Rows != 12 || res.Period != "2026" {
		t.Errorf("result = %+v", res)
	}
	want := recordedPut{"ieso_genoutputbyfuelhourly", "PUB_GenOutputbyFuelHourly_2026_20261108T115056Z.xml", "application/xml"}
	if len(store.puts) != 1 || store.puts[0] != want {
		t.Errorf("puts = %+v, want %+v", store.puts, want)
	}
	if !IsYearly("GenOutputbyFuelHourly") {
		t.Error("GenOutputbyFuelHourly should be a yearly report")
	}
}
