// Package timeutil converts the wall-clock times that grid sources publish into
// UTC, and says explicitly when a conversion is not clean.
package timeutil

import "time"

// Resolution says how cleanly a wall-clock time mapped to an instant.
type Resolution int

const (
	// Unique means the wall-clock time maps to exactly one instant.
	Unique Resolution = iota
	// Ambiguous means the time happens twice (clocks fall back). The earlier
	// instant is returned; the source gives no way to tell which was meant.
	Ambiguous
	// Nonexistent means the time never happens (clocks spring forward). The
	// instant is computed with the offset in force before the change.
	Nonexistent
)

func (r Resolution) String() string {
	switch r {
	case Unique:
		return "unique"
	case Ambiguous:
		return "ambiguous"
	case Nonexistent:
		return "nonexistent"
	}
	return "unknown"
}

// LocalToUTC interprets wall, a wall-clock time held in the fields of a UTC
// time.Time (what time.Parse returns for a string with no zone), in loc.
func LocalToUTC(loc *time.Location, wall time.Time) (time.Time, Resolution) {
	// The wall clock and the real instant differ by less than a day, so the
	// offsets 36h either side of it bracket at most one transition.
	_, before := wall.Add(-36 * time.Hour).In(loc).Zone()
	_, after := wall.Add(36 * time.Hour).In(loc).Zone()

	var valid []time.Time
	for _, off := range []int{before, after} {
		utc := wall.Add(-time.Duration(off) * time.Second)
		if sameWall(utc.In(loc), wall) && (len(valid) == 0 || !valid[0].Equal(utc)) {
			valid = append(valid, utc)
		}
	}
	switch len(valid) {
	case 0:
		return wall.Add(-time.Duration(before) * time.Second), Nonexistent
	case 1:
		return valid[0], Unique
	default:
		first := valid[0]
		if valid[1].Before(first) {
			first = valid[1]
		}
		return first, Ambiguous
	}
}

// OffsetMatches reports whether a source's UTC time and its local wall-clock
// time agree with loc's real daylight saving rules. It is how pipeline health
// catches a source whose two timestamp fields disagree.
func OffsetMatches(loc *time.Location, utc, localWall time.Time) bool {
	return sameWall(utc.In(loc), localWall)
}

func sameWall(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day() &&
		a.Hour() == b.Hour() && a.Minute() == b.Minute() && a.Second() == b.Second()
}
