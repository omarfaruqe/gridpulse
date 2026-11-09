package ieso

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func rtFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/RealtimeOntarioZonalPrice.xml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseRealtimeOntarioZonalPrice(t *testing.T) {
	h, err := ParseRealtimeOntarioZonalPrice(rtFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if h.HourEnding != 14 || h.Day.Format("2006-01-02") != "2026-11-09" {
		t.Errorf("hour = %s hour %d", h.Day.Format("2006-01-02"), h.HourEnding)
	}
	// CreatedAt is in EST: 13:28:09 EST = 18:28:09 UTC.
	if want := time.Date(2026, 11, 9, 18, 28, 9, 0, time.UTC); !h.Created.Equal(want) {
		t.Errorf("Created = %v, want %v", h.Created, want)
	}
	if len(h.Intervals) != 7 {
		t.Fatalf("intervals = %d, want 7 (intervals 8 to 12 are not published yet)", len(h.Intervals))
	}
	first, last := h.Intervals[0], h.Intervals[6]
	// Hour ending 14 covers 13:00-14:00 EST = 18:00-19:00 UTC.
	if want := time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC); !first.IntervalStartUTC.Equal(want) {
		t.Errorf("first start = %v, want %v", first.IntervalStartUTC, want)
	}
	if want := time.Date(2026, 11, 9, 18, 30, 0, 0, time.UTC); !last.IntervalStartUTC.Equal(want) {
		t.Errorf("interval 7 start = %v, want %v", last.IntervalStartUTC, want)
	}
	if first.ZonalPrice != 54.35 || first.LossPrice == nil || *first.LossPrice != -0.40 ||
		first.CongestionPrice == nil || *first.CongestionPrice != 0.39 || first.Flag != "DSO-RD" {
		t.Errorf("first interval = %+v", first)
	}
}

func TestParseRealtimeRejectsDrift(t *testing.T) {
	good := string(rtFixture(t))
	empty12 := "<ZonalPrice>\n<Interval>12</Interval>\n<Flag/>\n<LmpCap/>\n<LossPriceCap/>\n<CongPriceCap/>\n</ZonalPrice>\n"
	for name, in := range map[string]string{
		"not xml":           "nope",
		"bad date":          strings.Replace(good, "2026-11-09</DeliveryDate>", "x</DeliveryDate>", 1),
		"hour 25":           strings.Replace(good, "<DeliveryHour>14<", "<DeliveryHour>25<", 1),
		"bad created":       strings.Replace(good, "2026-11-09T13:28:09", "yesterday", 1),
		"bad price":         strings.Replace(good, "<LmpCap>54.35<", "<LmpCap>abc<", 1),
		"repeated interval": strings.Replace(good, "<Interval>2<", "<Interval>1<", 1),
		"components only": strings.Replace(good, "<Interval>8</Interval>\n<Flag/>\n<LmpCap/>\n<LossPriceCap/>",
			"<Interval>8</Interval>\n<Flag/>\n<LmpCap/>\n<LossPriceCap>1.0</LossPriceCap>", 1),
		"fewer intervals": strings.Replace(good, empty12, "", 1),
	} {
		if in == good {
			t.Fatalf("%s: the test edit did not change the fixture", name)
		}
		if _, err := ParseRealtimeOntarioZonalPrice([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// blankPrices makes the file look like an hour whose first interval is not out yet.
func blankPrices(s string) string {
	for _, v := range []string{"54.35", "71.93", "116.38", "143.09", "149.38", "143.59", "149.37"} {
		s = strings.Replace(s, "<LmpCap>"+v+"</LmpCap>", "<LmpCap/>", 1)
	}
	for _, v := range []string{"-0.40", "-0.54", "-0.89", "-1.10", "-1.15", "-1.09", "-1.16"} {
		s = strings.Replace(s, "<LossPriceCap>"+v+"</LossPriceCap>", "<LossPriceCap/>", 1)
	}
	for _, v := range []string{"0.39", "0.36", "0.07", "0.06", "0.05"} {
		s = strings.ReplaceAll(s, "<CongPriceCap>"+v+"</CongPriceCap>", "<CongPriceCap/>")
	}
	return strings.ReplaceAll(s, "<Flag>DSO-RD</Flag>", "<Flag/>")
}

func TestFetchHourStoresSnapshotAndRejectsEmptyOrMismatched(t *testing.T) {
	data := rtFixture(t)
	nothingYet := blankPrices(string(data))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/PUB_RealtimeOntarioZonalPrice_2026110914.xml"):
			_, _ = w.Write(data)
		case strings.HasSuffix(r.URL.Path, "/PUB_RealtimeOntarioZonalPrice_2026110915.xml"):
			_, _ = w.Write(data) // holds hour 14, not 15
		case strings.HasSuffix(r.URL.Path, "/PUB_RealtimeOntarioZonalPrice_2026110913.xml"):
			_, _ = w.Write([]byte(strings.Replace(nothingYet, "<DeliveryHour>14<", "<DeliveryHour>13<", 1)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	store := &fakeStore{objects: map[string][]byte{}}
	ex := newTestExtractor(srv, store)
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

	res, err := ex.FetchHour(t.Context(), day, 14)
	if err != nil || res.Rows != 7 {
		t.Fatalf("FetchHour = %+v, %v", res, err)
	}
	wantKey := "source=ieso_realtimeontariozonalprice/date=2026-11-09/PUB_RealtimeOntarioZonalPrice_2026110914_20261109T182809Z.xml"
	if res.Key != wantKey {
		t.Errorf("key = %q, want %q", res.Key, wantKey)
	}
	if res2, err := ex.FetchHour(t.Context(), day, 14); err != nil || res2.Key != wantKey || len(store.objects) != 1 {
		t.Errorf("the same upstream version must map to the same key: %+v %v (%d objects)", res2, err, len(store.objects))
	}

	if _, err := ex.FetchHour(t.Context(), day, 13); !errors.Is(err, ErrNotPublished) {
		t.Errorf("a file with no priced interval = %v, want ErrNotPublished", err)
	}
	if _, err := ex.FetchHour(t.Context(), day, 15); err == nil || errors.Is(err, ErrNotPublished) {
		t.Errorf("a file holding another hour = %v, want a mismatch error", err)
	}
	if _, err := ex.FetchHour(t.Context(), day, 12); !errors.Is(err, ErrNotPublished) {
		t.Errorf("a 404 = %v, want ErrNotPublished", err)
	}
	if len(store.objects) != 1 {
		t.Errorf("objects = %d, want only the good file stored", len(store.objects))
	}
	if _, err := ex.FetchHour(t.Context(), day, 25); err == nil {
		t.Error("hour 25 must be rejected")
	}
}
