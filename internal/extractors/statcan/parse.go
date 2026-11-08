// Package statcan extracts Statistics Canada table 25-10-0015-01, monthly
// electricity generation by type and class of producer, for reconciliation.
package statcan

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	wantUOM    = "Megawatt hours"
	wantScalar = "units"
)

// requiredColumns are looked up by name, so a reordered file still parses and a
// renamed or dropped column fails loudly instead of shifting every value.
var requiredColumns = []string{
	"REF_DATE", "GEO", "Class of electricity producer", "Type of electricity generation",
	"UOM", "SCALAR_FACTOR", "VECTOR", "VALUE", "STATUS",
}

// LoadedGeographies are the geographies kept in staging.
var LoadedGeographies = map[string]bool{"Ontario": true, "Quebec": true, "Alberta": true}

// Observation is one monthly generation figure.
type Observation struct {
	Month          time.Time // first day of the reference month, UTC
	Geo            string
	ProducerClass  string
	GenerationType string
	MWh            *float64 // nil when StatCan publishes no value (see Status)
	Status         string   // StatCan status symbol, e.g. ".." not available, "E" use with caution
	Vector         string
}

// ParseGeneration decodes the table's CSV. It rejects a file whose unit or
// scale is not megawatt hours as plain units, because a silent change there
// would make every comparison wrong by a constant factor.
func ParseGeneration(data []byte) ([]Observation, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data)) // FieldsPerRecord 0: every row must match the header
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("statcan: read header: %w", err)
	}
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	for _, name := range requiredColumns {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("statcan: column %q is missing (schema drift?)", name)
		}
	}

	type key struct{ month, geo, class, kind string }
	seen := map[key]bool{}
	var out []Observation
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("statcan: line %d: %w", line, err)
		}
		get := func(name string) string { return strings.TrimSpace(rec[col[name]]) }

		if uom := get("UOM"); uom != wantUOM {
			return nil, fmt.Errorf("statcan: line %d has unit %q, want %q", line, uom, wantUOM)
		}
		if sf := get("SCALAR_FACTOR"); sf != wantScalar {
			return nil, fmt.Errorf("statcan: line %d has scale %q, want %q", line, sf, wantScalar)
		}
		month, err := time.Parse("2006-01", get("REF_DATE"))
		if err != nil {
			return nil, fmt.Errorf("statcan: line %d has bad REF_DATE %q: %w", line, get("REF_DATE"), err)
		}
		var mwh *float64
		if v := get("VALUE"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("statcan: line %d has bad VALUE %q: %w", line, v, err)
			}
			mwh = &f
		}
		o := Observation{
			Month: month, Geo: get("GEO"),
			ProducerClass: get("Class of electricity producer"), GenerationType: get("Type of electricity generation"),
			MWh: mwh, Status: get("STATUS"), Vector: get("VECTOR"),
		}
		k := key{get("REF_DATE"), o.Geo, o.ProducerClass, o.GenerationType}
		if seen[k] {
			return nil, fmt.Errorf("statcan: line %d repeats %v", line, k)
		}
		seen[k] = true
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil, errors.New("statcan: file has no data rows (schema drift?)")
	}
	return out, nil
}
