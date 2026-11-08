package ieso

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type fuelDoc struct {
	CreatedAt string `xml:"DocHeader>CreatedAt"`
	Year      string `xml:"DocBody>DeliveryYear"`
	Days      []struct {
		Day   string `xml:"Day"`
		Hours []struct {
			Hour  int `xml:"Hour"`
			Fuels []struct {
				Fuel    string `xml:"Fuel"`
				Quality string `xml:"EnergyValue>OutputQuality"` // strings: an absent element must stay nil, not 0
				Output  string `xml:"EnergyValue>Output"`
			} `xml:"FuelTotal"`
		} `xml:"HourlyData"`
	} `xml:"DocBody>DailyData"`
}

// FuelHour is the total output of one fuel type for one hour.
type FuelHour struct {
	Fuel             string
	HourEnding       int       // 1..24 as published
	IntervalStartUTC time.Time // start of the hour, in UTC
	OutputMW         *float64  // nil when IESO published no value
	// Quality is IESO's "flag to indicate number of data points not available".
	// The schema does not list its values, so it is stored as published and not interpreted.
	Quality *int
}

// ParseGenOutputbyFuelHourly decodes a yearly Generator Output by Fuel Type
// Hourly report and returns its rows plus the report's CreatedAt time.
//
// Day and Hour are read as IESO's other reports are (hour ending, fixed EST).
// The schema does not say so; the cross-check against the per-generator report
// in the docs is what supports it.
func ParseGenOutputbyFuelHourly(data []byte) ([]FuelHour, time.Time, error) {
	var doc fuelDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, time.Time{}, fmt.Errorf("ieso: decode GenOutputbyFuelHourly: %w", err)
	}
	created, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(doc.CreatedAt), EST)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("ieso: bad CreatedAt %q (schema drift?): %w", doc.CreatedAt, err)
	}
	year, err := strconv.Atoi(strings.TrimSpace(doc.Year))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("ieso: bad DeliveryYear %q (schema drift?): %w", doc.Year, err)
	}
	if len(doc.Days) == 0 {
		return nil, time.Time{}, fmt.Errorf("ieso: GenOutputbyFuelHourly has no days (schema drift?)")
	}

	type key struct {
		day  string
		hour int
		fuel string
	}
	seen := map[key]bool{}
	var rows []FuelHour
	for _, d := range doc.Days {
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(d.Day), EST)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("ieso: bad Day %q (schema drift?): %w", d.Day, err)
		}
		if day.Year() != year {
			return nil, time.Time{}, fmt.Errorf("ieso: day %s is outside delivery year %d", d.Day, year)
		}
		for _, h := range d.Hours {
			if h.Hour < 1 || h.Hour > 24 {
				return nil, time.Time{}, fmt.Errorf("ieso: day %s has invalid hour %d", d.Day, h.Hour)
			}
			for _, f := range h.Fuels {
				fuel := strings.TrimSpace(f.Fuel)
				if fuel == "" {
					return nil, time.Time{}, fmt.Errorf("ieso: day %s hour %d has a fuel total without a fuel", d.Day, h.Hour)
				}
				k := key{d.Day, h.Hour, fuel}
				if seen[k] {
					return nil, time.Time{}, fmt.Errorf("ieso: duplicate %s total for %s hour %d", fuel, d.Day, h.Hour)
				}
				seen[k] = true

				out, err := optionalFloat(f.Output)
				if err != nil {
					return nil, time.Time{}, fmt.Errorf("ieso: %s %s hour %d has bad Output %q: %w", fuel, d.Day, h.Hour, f.Output, err)
				}
				var quality *int
				if q := strings.TrimSpace(f.Quality); q != "" {
					v, err := strconv.Atoi(q)
					if err != nil {
						return nil, time.Time{}, fmt.Errorf("ieso: %s %s hour %d has bad OutputQuality %q: %w", fuel, d.Day, h.Hour, f.Quality, err)
					}
					quality = &v
				}
				rows = append(rows, FuelHour{
					Fuel: fuel, HourEnding: h.Hour,
					IntervalStartUTC: day.Add(time.Duration(h.Hour-1) * time.Hour).UTC(),
					OutputMW:         out, Quality: quality,
				})
			}
		}
	}
	if len(rows) == 0 {
		return nil, time.Time{}, fmt.Errorf("ieso: GenOutputbyFuelHourly has no fuel totals (schema drift?)")
	}
	return rows, created, nil
}
