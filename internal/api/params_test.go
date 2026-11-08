package api

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func q(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

func TestParseRangeDefaultsToLastSevenDays(t *testing.T) {
	s, err := parseRange(q(), now)
	if err != nil {
		t.Fatal(err)
	}
	if s.fromDay != "2026-11-02" || s.lastDay != "2026-11-08" {
		t.Errorf("range = %s..%s, want 2026-11-02..2026-11-08", s.fromDay, s.lastDay)
	}
	if !s.from.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) || !s.to.Equal(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("bounds = %v .. %v, want from inclusive and to exclusive (the day after the last day)", s.from, s.to)
	}
}

func TestParseRangeExplicit(t *testing.T) {
	cases := []struct {
		name       string
		params     url.Values
		from, last string
	}{
		{"both", q("from", "2025-01-01", "to", "2025-01-31"), "2025-01-01", "2025-01-31"},
		{"to only: seven days ending there", q("to", "2025-03-10"), "2025-03-04", "2025-03-10"},
		{"from only: runs to today", q("from", "2026-11-05"), "2026-11-05", "2026-11-08"},
		{"one day", q("from", "2025-06-01", "to", "2025-06-01"), "2025-06-01", "2025-06-01"},
		{"exactly the maximum", q("from", "2025-01-01", "to", "2025-04-02"), "2025-01-01", "2025-04-02"}, // 92 days
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := parseRange(c.params, now)
			if err != nil {
				t.Fatal(err)
			}
			if s.fromDay != c.from || s.lastDay != c.last {
				t.Errorf("range = %s..%s, want %s..%s", s.fromDay, s.lastDay, c.from, c.last)
			}
		})
	}
}

func TestParseRangeRejects(t *testing.T) {
	cases := map[string]url.Values{
		"not a date":       q("from", "yesterday"),
		"wrong format":     q("to", "08/11/2026"),
		"reversed":         q("from", "2026-11-05", "to", "2026-11-01"),
		"one day too long": q("from", "2025-01-01", "to", "2025-04-03"), // 93 days
		"years":            q("from", "2020-01-01", "to", "2026-01-01"),
		"from in future":   q("from", "2027-01-01"),
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseRange(params, now)
			if err == nil {
				t.Fatal("expected an error")
			}
			if _, ok := err.(badRequest); !ok {
				t.Errorf("error %T is not a badRequest the client can see", err)
			}
		})
	}
}

func TestParseProvince(t *testing.T) {
	for _, p := range provinces {
		if got, err := parseProvince(q("province", p)); err != nil || got != p {
			t.Errorf("%s = %q, %v", p, got, err)
		}
	}
	for _, bad := range []string{"", "on", "XX", "ON; DROP TABLE x", "ON "} {
		_, err := parseProvince(q("province", bad))
		if err == nil {
			t.Errorf("province %q accepted", bad)
		} else if bad == "" && !strings.Contains(err.Error(), "required") {
			t.Errorf("missing province error = %q, want it to say required", err)
		}
	}
}
