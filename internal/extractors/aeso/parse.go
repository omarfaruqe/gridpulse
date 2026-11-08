// Package aeso extracts AESO (Alberta) pool price and load from its public API.
package aeso

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed tz database so containers without tzdata still work
)

// Mountain is Alberta's local time zone, used to pick "yesterday" for a daily run.
var Mountain = mustLoadLocation("America/Edmonton")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type envelope struct {
	ResponseCode string                     `json:"responseCode"`
	Return       map[string]json.RawMessage `json:"return"`
}

// decodeRows reads the report array stored under return[key].
func decodeRows(data []byte, key string, rows any) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("aeso: decode response: %w", err)
	}
	if env.ResponseCode != "" && env.ResponseCode != "200" {
		return fmt.Errorf("aeso: responseCode %q", env.ResponseCode)
	}
	raw, ok := env.Return[key]
	if !ok {
		return fmt.Errorf("aeso: response has no %q (schema drift?)", key)
	}
	if err := json.Unmarshal(raw, rows); err != nil {
		return fmt.Errorf("aeso: decode %q: %w", key, err)
	}
	return nil
}

// parseStartUTC reads begin_datetime_utc. The begin_datetime_mpt field is
// deliberately ignored: it disagrees with Alberta's real UTC offset.
func parseStartUTC(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(s), time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("aeso: bad begin_datetime_utc %q: %w", s, err)
	}
	return t, nil
}

// optionalFloat turns "" into nil so a blank is never mistaken for zero.
func optionalFloat(field, s string) (*float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("aeso: bad %s %q: %w", field, s, err)
	}
	return &v, nil
}

// PoolPriceHour is one hour of the Alberta pool price, in $/MWh.
type PoolPriceHour struct {
	StartUTC          time.Time
	PoolPrice         *float64 // nil until the hour has settled
	ForecastPoolPrice *float64
	Rolling30DayAvg   *float64
}

// ParsePoolPrice decodes a Pool Price Report response.
func ParsePoolPrice(data []byte) ([]PoolPriceHour, error) {
	var raw []struct {
		BeginUTC string `json:"begin_datetime_utc"`
		Price    string `json:"pool_price"`
		Forecast string `json:"forecast_pool_price"`
		Rolling  string `json:"rolling_30day_avg"`
	}
	if err := decodeRows(data, "Pool Price Report", &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("aeso: Pool Price Report has no rows")
	}
	rows := make([]PoolPriceHour, 0, len(raw))
	for _, r := range raw {
		start, err := parseStartUTC(r.BeginUTC)
		if err != nil {
			return nil, err
		}
		row := PoolPriceHour{StartUTC: start}
		if row.PoolPrice, err = optionalFloat("pool_price", r.Price); err != nil {
			return nil, err
		}
		if row.ForecastPoolPrice, err = optionalFloat("forecast_pool_price", r.Forecast); err != nil {
			return nil, err
		}
		if row.Rolling30DayAvg, err = optionalFloat("rolling_30day_avg", r.Rolling); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// LoadHour is one hour of Alberta internal load, in MW.
type LoadHour struct {
	StartUTC       time.Time
	LoadMW         *float64 // nil for hours that have not happened yet
	ForecastLoadMW *float64
}

// ParseAlbertaInternalLoad decodes an Actual Forecast Report response.
func ParseAlbertaInternalLoad(data []byte) ([]LoadHour, error) {
	var raw []struct {
		BeginUTC string `json:"begin_datetime_utc"`
		Load     string `json:"alberta_internal_load"`
		Forecast string `json:"forecast_alberta_internal_load"`
	}
	if err := decodeRows(data, "Actual Forecast Report", &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("aeso: Actual Forecast Report has no rows")
	}
	rows := make([]LoadHour, 0, len(raw))
	for _, r := range raw {
		start, err := parseStartUTC(r.BeginUTC)
		if err != nil {
			return nil, err
		}
		row := LoadHour{StartUTC: start}
		if row.LoadMW, err = optionalFloat("alberta_internal_load", r.Load); err != nil {
			return nil, err
		}
		if row.ForecastLoadMW, err = optionalFloat("forecast_alberta_internal_load", r.Forecast); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
