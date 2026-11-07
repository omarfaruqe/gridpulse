package ieso

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func demandFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/Demand.csv")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseDemand(t *testing.T) {
	rows, created, err := ParseDemand(demandFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// Created at 07:30:15 EST = 12:30:15 UTC.
	if want := time.Date(2026, 11, 7, 12, 30, 15, 0, time.UTC); !created.UTC().Equal(want) {
		t.Errorf("created = %v, want %v", created.UTC(), want)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Hour 1 of 2026-01-01 starts 00:00 EST = 05:00 UTC.
	if want := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC); !rows[0].IntervalStartUTC.Equal(want) {
		t.Errorf("IntervalStartUTC = %v, want %v", rows[0].IntervalStartUTC, want)
	}
	if rows[0].OntarioDemandMW == nil || *rows[0].OntarioDemandMW != 16526 {
		t.Errorf("Ontario demand = %v, want 16526", rows[0].OntarioDemandMW)
	}
	if rows[0].MarketDemandMW == nil || *rows[0].MarketDemandMW != 19489 {
		t.Errorf("Market demand = %v, want 19489", rows[0].MarketDemandMW)
	}
	if rows[2].OntarioDemandMW != nil || rows[2].MarketDemandMW != nil {
		t.Error("blank values should be nil")
	}
}

func TestParseDemandRejectsDrift(t *testing.T) {
	const created = "\\\\Created at 2026-11-07 07:30:15,,,\n"
	const header = "Date,Hour,Market Demand,Ontario Demand\n"
	for name, in := range map[string]string{
		"no created-at": header + "2026-01-01,1,1,1\n",
		"bad created":   "\\\\Created at yesterday,,,\n" + header + "2026-01-01,1,1,1\n",
		"wrong header":  created + "Date,Hour,Demand,Other\n2026-01-01,1,1,1\n",
		"no rows":       created + header,
		"bad hour":      created + header + "2026-01-01,25,1,1\n",
		"bad date":      created + header + "01/01/2026,1,1,1\n",
		"bad number":    created + header + "2026-01-01,1,abc,1\n",
		"extra column":  created + header + "2026-01-01,1,1,1,9\n",
	} {
		if _, _, err := ParseDemand([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestFetchYearIsIdempotentAndVersioned(t *testing.T) {
	data := demandFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Demand/PUB_Demand_2026.csv" {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)

	for i := 0; i < 2; i++ { // same upstream version twice: one object
		res, err := ex.FetchYear(context.Background(), "Demand", 2026)
		if err != nil {
			t.Fatal(err)
		}
		if res.Rows != 3 || res.Period != "2026" {
			t.Errorf("result = %+v", res)
		}
	}
	key := "source=ieso_demand/date=2026-11-07/PUB_Demand_2026_20261107T123015Z.csv"
	if len(store.objects) != 1 || !bytes.Equal(store.objects[key], data) {
		t.Errorf("objects = %d, want 1 raw object at %q", len(store.objects), key)
	}

	stored, missing, err := ex.BackfillYears(context.Background(), "Demand", 2025, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || len(missing) != 1 || missing[0] != 2025 {
		t.Errorf("stored=%d missing=%v, want 1 stored and [2025]", len(stored), missing)
	}
}

func TestIsYearly(t *testing.T) {
	if !IsYearly("Demand") || IsYearly("GenOutputCapability") {
		t.Error("IsYearly misclassified a report")
	}
}
