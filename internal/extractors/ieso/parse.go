// Package ieso extracts IESO (Ontario) public reports.
package ieso

import (
	"encoding/xml"
	"fmt"
	"time"
)

// EST is the fixed reporting zone IESO uses all year: UTC-5, no daylight saving.
var EST = time.FixedZone("EST", -5*3600)

type genDoc struct {
	Date       string `xml:"IMODocBody>Date"`
	Generators []struct {
		Name    string `xml:"GeneratorName"`
		Fuel    string `xml:"FuelType"`
		Outputs []struct {
			Hour   int      `xml:"Hour"`
			Energy *float64 `xml:"EnergyMW"`
		} `xml:"Outputs>Output"`
	} `xml:"IMODocBody>Generators>Generator"`
}

// GeneratorHour is one generator's output for one hour.
type GeneratorHour struct {
	Generator        string
	Fuel             string
	HourEnding       int       // 1..24 as published (hour 1 covers 00:00-01:00 EST)
	IntervalStartUTC time.Time // start of the hour, in UTC
	EnergyMW         *float64  // nil when IESO published no value
}

// ParseGenOutputCapability decodes a GenOutputCapability daily report.
func ParseGenOutputCapability(data []byte) ([]GeneratorHour, error) {
	var doc genDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("ieso: decode GenOutputCapability: %w", err)
	}
	day, err := time.ParseInLocation("2006-01-02", doc.Date, EST)
	if err != nil {
		return nil, fmt.Errorf("ieso: bad report date %q (schema drift?): %w", doc.Date, err)
	}
	if len(doc.Generators) == 0 {
		return nil, fmt.Errorf("ieso: GenOutputCapability has no generators (schema drift?)")
	}
	var rows []GeneratorHour
	for _, g := range doc.Generators {
		for _, o := range g.Outputs {
			if o.Hour < 1 || o.Hour > 24 {
				return nil, fmt.Errorf("ieso: generator %s has invalid hour %d", g.Name, o.Hour)
			}
			start := day.Add(time.Duration(o.Hour-1) * time.Hour)
			rows = append(rows, GeneratorHour{
				Generator: g.Name, Fuel: g.Fuel, HourEnding: o.Hour,
				IntervalStartUTC: start.UTC(), EnergyMW: o.Energy,
			})
		}
	}
	return rows, nil
}
