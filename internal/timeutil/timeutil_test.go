package timeutil

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func wall(y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, time.UTC)
}

func TestLocalToUTC(t *testing.T) {
	toronto := mustLoad(t, "America/Toronto")
	edmonton := mustLoad(t, "America/Edmonton")

	tests := []struct {
		name string
		loc  *time.Location
		wall time.Time
		want time.Time
		res  Resolution
	}{
		{"summer day", toronto, wall(2026, 7, 1, 12, 0), wall(2026, 7, 1, 16, 0), Unique},
		{"winter day", toronto, wall(2026, 1, 15, 12, 0), wall(2026, 1, 15, 17, 0), Unique},
		{"before fall-back", toronto, wall(2026, 11, 1, 0, 30), wall(2026, 11, 1, 4, 30), Unique},
		{"fall-back repeated hour", toronto, wall(2026, 11, 1, 1, 30), wall(2026, 11, 1, 5, 30), Ambiguous},
		{"after fall-back", toronto, wall(2026, 11, 1, 2, 30), wall(2026, 11, 1, 7, 30), Unique},
		{"before spring-forward", toronto, wall(2026, 3, 8, 1, 30), wall(2026, 3, 8, 6, 30), Unique},
		{"spring-forward gap", toronto, wall(2026, 3, 8, 2, 30), wall(2026, 3, 8, 7, 30), Nonexistent},
		{"after spring-forward", toronto, wall(2026, 3, 8, 3, 30), wall(2026, 3, 8, 7, 30), Unique},

		// Alberta had a real fall-back on 2025-11-02, then adopted Alberta Time
		// (UTC-6 all year) on 2026-11-01, so history crosses a rule change.
		{"alberta 2025 repeated hour", edmonton, wall(2025, 11, 2, 1, 30), wall(2025, 11, 2, 7, 30), Ambiguous},
		{"alberta standard time, Dec 2025", edmonton, wall(2025, 12, 1, 0, 0), wall(2025, 12, 1, 7, 0), Unique},
		{"alberta 2026-11-01 has no repeated hour", edmonton, wall(2026, 11, 1, 1, 30), wall(2026, 11, 1, 7, 30), Unique},
		{"alberta time after the change", edmonton, wall(2026, 11, 6, 0, 0), wall(2026, 11, 6, 6, 0), Unique},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, res := LocalToUTC(tt.loc, tt.wall)
			if res != tt.res {
				t.Errorf("resolution = %v, want %v", res, tt.res)
			}
			if !got.Equal(tt.want) {
				t.Errorf("utc = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOffsetMatches(t *testing.T) {
	edmonton := mustLoad(t, "America/Edmonton")
	tests := []struct {
		name  string
		utc   time.Time
		local time.Time
		want  bool
	}{
		{"Alberta Time (UTC-6) after 2026-11-01", wall(2026, 11, 6, 6, 0), wall(2026, 11, 6, 0, 0), true},
		{"standard time (UTC-7) before the change", wall(2025, 12, 1, 7, 0), wall(2025, 12, 1, 0, 0), true},
		{"summer daylight time", wall(2026, 7, 6, 6, 0), wall(2026, 7, 6, 0, 0), true},
		{"offset that disagrees with the rules", wall(2025, 12, 1, 6, 0), wall(2025, 12, 1, 0, 0), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OffsetMatches(edmonton, tt.utc, tt.local); got != tt.want {
				t.Errorf("OffsetMatches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolutionString(t *testing.T) {
	if Unique.String() != "unique" || Ambiguous.String() != "ambiguous" || Nonexistent.String() != "nonexistent" {
		t.Error("unexpected Resolution names")
	}
}
