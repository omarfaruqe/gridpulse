package bronze

import (
	"testing"
	"time"
)

func TestKey(t *testing.T) {
	// 23:30 in Montreal on Jan 31 is already Feb 1 in UTC.
	mtl := time.FixedZone("EST", -5*3600)
	late := time.Date(2026, 1, 31, 23, 30, 0, 0, mtl)

	tests := []struct {
		name     string
		source   string
		date     time.Time
		filename string
		want     string
		wantErr  bool
	}{
		{"basic", "hydroquebec", time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), "demand.json", "source=hydroquebec/date=2026-11-07/demand.json", false},
		{"normalizes to UTC", "ieso", late, "demand.xml", "source=ieso/date=2026-02-01/demand.xml", false},
		{"empty source", "", time.Now(), "a.json", "", true},
		{"empty filename", "ieso", time.Now(), "", "", true},
		{"slash in filename", "ieso", time.Now(), "a/b.json", "", true},
		{"slash in source", "a/b", time.Now(), "a.json", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Key(tt.source, tt.date, tt.filename)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeyIdempotent(t *testing.T) {
	d := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	a, _ := Key("aeso", d, "pool_price.json")
	b, _ := Key("aeso", d, "pool_price.json")
	if a != b {
		t.Errorf("same inputs gave different keys: %q vs %q", a, b)
	}
}
