// Package hydroquebec extracts Hydro-Quebec's real-time demand feed.
package hydroquebec

import (
	"encoding/json"
	"fmt"
	"time"
	_ "time/tzdata" // embed tz database so containers without tzdata still work

	"github.com/omarfaruqe/gridpulse/internal/timeutil"
)

// Source is the bronze partition name for this feed.
const Source = "hydroquebec_demand"

const localLayout = "2006-01-02T15:04:05"

var eastern = mustLoadLocation("America/Toronto")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type feed struct {
	Details []struct {
		Date    string `json:"date"`
		Valeurs struct {
			DemandeTotal *float64 `json:"demandeTotal"`
		} `json:"valeurs"`
	} `json:"details"`
}

// Observation is one 15-minute slot of the feed.
type Observation struct {
	LocalTime string    // raw timestamp from the feed (Eastern, no offset)
	TimeUTC   time.Time // LocalTime interpreted as America/Toronto, in UTC
	DemandMW  *float64  // nil for slots that have no value yet

	// Resolution is Ambiguous for the repeated hour when clocks fall back and
	// Nonexistent for the skipped hour when they spring forward.
	Resolution timeutil.Resolution
}

// Parse decodes a raw feed snapshot. It does not modify the raw bytes.
func Parse(data []byte) ([]Observation, error) {
	var f feed
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("hydroquebec: decode feed: %w", err)
	}
	if len(f.Details) == 0 {
		return nil, fmt.Errorf("hydroquebec: feed has no details (schema drift?)")
	}
	obs := make([]Observation, 0, len(f.Details))
	for _, d := range f.Details {
		wall, err := time.Parse(localLayout, d.Date) // wall clock in UTC fields
		if err != nil {
			return nil, fmt.Errorf("hydroquebec: bad timestamp %q: %w", d.Date, err)
		}
		utc, res := timeutil.LocalToUTC(eastern, wall)
		obs = append(obs, Observation{LocalTime: d.Date, TimeUTC: utc, DemandMW: d.Valeurs.DemandeTotal, Resolution: res})
	}
	return obs, nil
}
