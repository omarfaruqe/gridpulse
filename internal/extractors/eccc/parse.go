package eccc

import (
	"encoding/json"
	"fmt"
	"time"
)

type props struct {
	StationName         string   `json:"STATION_NAME"`
	ClimateID           string   `json:"CLIMATE_IDENTIFIER"`
	Province            string   `json:"PROVINCE_CODE"`
	UTCDate             string   `json:"UTC_DATE"`
	Temp                *float64 `json:"TEMP"`
	TempFlag            *string  `json:"TEMP_FLAG"`
	DewPoint            *float64 `json:"DEW_POINT_TEMP"`
	RelativeHumidity    *float64 `json:"RELATIVE_HUMIDITY"`
	RelativeHumidityFlg *string  `json:"RELATIVE_HUMIDITY_FLAG"`
	WindSpeed           *float64 `json:"WIND_SPEED"`
	WindSpeedFlag       *string  `json:"WIND_SPEED_FLAG"`
	WindDirection       *float64 `json:"WIND_DIRECTION"`
	StationPressure     *float64 `json:"STATION_PRESSURE"`
	Visibility          *float64 `json:"VISIBILITY"`
	PrecipAmount        *float64 `json:"PRECIP_AMOUNT"`
}

type featureCollection struct {
	Type           string `json:"type"`
	NumberMatched  int    `json:"numberMatched"`
	NumberReturned int    `json:"numberReturned"`
	Features       []struct {
		Properties props `json:"properties"`
	} `json:"features"`
}

// Observation is one hourly weather observation. Measurements are in the
// units ECCC publishes; a nil value means ECCC published no value, never zero.
// The *Flag fields carry ECCC's quality flag ("" when there is none).
type Observation struct {
	ClimateID   string
	StationName string
	Province    string
	TimeUTC     time.Time

	Temp             *float64
	TempFlag         string
	DewPoint         *float64
	RelativeHumidity *float64
	RelHumidityFlag  string
	WindSpeed        *float64
	WindSpeedFlag    string
	WindDirection    *float64
	StationPressure  *float64
	Visibility       *float64
	PrecipAmount     *float64
}

// ParseHourly decodes a climate-hourly GeoJSON response.
func ParseHourly(data []byte) ([]Observation, error) {
	var fc featureCollection
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("eccc: decode response: %w", err)
	}
	if fc.Type != "FeatureCollection" {
		return nil, fmt.Errorf("eccc: response type is %q, want FeatureCollection (schema drift?)", fc.Type)
	}
	if fc.NumberMatched > len(fc.Features) {
		return nil, fmt.Errorf("eccc: response is truncated: %d matched but %d returned", fc.NumberMatched, len(fc.Features))
	}
	obs := make([]Observation, 0, len(fc.Features))
	seen := map[time.Time]bool{}
	for _, f := range fc.Features {
		p := f.Properties
		t, err := time.ParseInLocation("2006-01-02T15:04:05", p.UTCDate, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("eccc: bad UTC_DATE %q: %w", p.UTCDate, err)
		}
		if seen[t] {
			return nil, fmt.Errorf("eccc: duplicate observation for %s at %s", p.ClimateID, p.UTCDate)
		}
		seen[t] = true
		obs = append(obs, Observation{
			ClimateID: p.ClimateID, StationName: p.StationName, Province: p.Province, TimeUTC: t,
			Temp: p.Temp, TempFlag: deref(p.TempFlag),
			DewPoint:         p.DewPoint,
			RelativeHumidity: p.RelativeHumidity, RelHumidityFlag: deref(p.RelativeHumidityFlg),
			WindSpeed: p.WindSpeed, WindSpeedFlag: deref(p.WindSpeedFlag),
			WindDirection:   p.WindDirection,
			StationPressure: p.StationPressure,
			Visibility:      p.Visibility,
			PrecipAmount:    p.PrecipAmount,
		})
	}
	return obs, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
