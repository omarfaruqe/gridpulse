// Package eccc extracts Environment and Climate Change Canada hourly weather
// observations for a fixed set of stations that represent each grid region.
package eccc

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
)

//go:embed stations.json
var stationsJSON []byte

// Station maps one weather station to the grid region it represents.
type Station struct {
	ClimateID string  `json:"climate_id"` // ECCC CLIMATE_IDENTIFIER
	Name      string  `json:"name"`
	Province  string  `json:"province"`
	Region    string  `json:"region"`   // grid region this station represents
	Operator  string  `json:"operator"` // grid operator for that region
	Weight    float64 `json:"weight"`   // share of the region's weather; sums to 1 per region
}

// Stations returns the embedded station-to-region mapping, validated.
func Stations() ([]Station, error) {
	var stations []Station
	dec := json.NewDecoder(bytes.NewReader(stationsJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&stations); err != nil {
		return nil, fmt.Errorf("eccc: decode stations.json: %w", err)
	}
	if err := validateStations(stations); err != nil {
		return nil, err
	}
	return stations, nil
}

func validateStations(stations []Station) error {
	if len(stations) == 0 {
		return fmt.Errorf("eccc: no stations configured")
	}
	seen := map[string]bool{}
	sums := map[string]float64{}
	for _, s := range stations {
		if s.ClimateID == "" || s.Name == "" || s.Province == "" || s.Region == "" || s.Operator == "" {
			return fmt.Errorf("eccc: station %+v has an empty field", s)
		}
		if seen[s.ClimateID] {
			return fmt.Errorf("eccc: duplicate climate_id %s", s.ClimateID)
		}
		seen[s.ClimateID] = true
		if s.Weight <= 0 || s.Weight > 1 {
			return fmt.Errorf("eccc: station %s has weight %v outside (0, 1]", s.ClimateID, s.Weight)
		}
		sums[s.Region] += s.Weight
	}
	for region, sum := range sums {
		if math.Abs(sum-1) > 0.001 {
			return fmt.Errorf("eccc: weights for region %s sum to %v, want 1", region, sum)
		}
	}
	return nil
}
