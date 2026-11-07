package ieso

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DemandHour is one hour of the IESO hourly demand report.
type DemandHour struct {
	HourEnding       int       // 1..24 as published (hour 1 covers 00:00-01:00 EST)
	IntervalStartUTC time.Time // start of the hour, in UTC
	MarketDemandMW   *float64  // nil when no value was published
	OntarioDemandMW  *float64
}

var demandHeader = []string{"Date", "Hour", "Market Demand", "Ontario Demand"}

// ParseDemand decodes a yearly Demand CSV. It returns the rows and the
// "Created at" time from the file's comment header (the upstream version).
func ParseDemand(data []byte) ([]DemandHour, time.Time, error) {
	var created time.Time
	var body strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, `\\Created at `):
			ts := strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(line, `\\Created at `), ","))
			t, err := time.ParseInLocation("2006-01-02 15:04:05", ts, EST)
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("ieso: bad Demand created-at %q: %w", ts, err)
			}
			created = t
		case strings.HasPrefix(line, `\\`), strings.TrimSpace(line) == "":
			// comment or blank line
		default:
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	if created.IsZero() {
		return nil, time.Time{}, fmt.Errorf("ieso: Demand has no 'Created at' header (schema drift?)")
	}

	r := csv.NewReader(strings.NewReader(body.String()))
	r.FieldsPerRecord = len(demandHeader)
	records, err := r.ReadAll()
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("ieso: decode Demand csv: %w", err)
	}
	if len(records) < 2 {
		return nil, time.Time{}, fmt.Errorf("ieso: Demand has no data rows")
	}
	for i, want := range demandHeader {
		if strings.TrimSpace(records[0][i]) != want {
			return nil, time.Time{}, fmt.Errorf("ieso: Demand column %d is %q, want %q (schema drift?)", i+1, records[0][i], want)
		}
	}

	rows := make([]DemandHour, 0, len(records)-1)
	for _, rec := range records[1:] {
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(rec[0]), EST)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("ieso: bad Demand date %q: %w", rec[0], err)
		}
		hour, err := strconv.Atoi(strings.TrimSpace(rec[1]))
		if err != nil || hour < 1 || hour > 24 {
			return nil, time.Time{}, fmt.Errorf("ieso: bad Demand hour %q on %s", rec[1], rec[0])
		}
		market, err := optionalFloat(rec[2])
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("ieso: bad Market Demand %q on %s: %w", rec[2], rec[0], err)
		}
		ontario, err := optionalFloat(rec[3])
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("ieso: bad Ontario Demand %q on %s: %w", rec[3], rec[0], err)
		}
		rows = append(rows, DemandHour{
			HourEnding:       hour,
			IntervalStartUTC: day.Add(time.Duration(hour-1) * time.Hour).UTC(),
			MarketDemandMW:   market,
			OntarioDemandMW:  ontario,
		})
	}
	return rows, created, nil
}

func optionalFloat(s string) (*float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}
