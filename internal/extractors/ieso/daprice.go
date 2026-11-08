package ieso

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

type daPriceDoc struct {
	Date  string `xml:"DocBody>DeliveryDate"`
	Hours []struct {
		Hour       int    `xml:"PricingHour"`
		Price      string `xml:"ZonalPrice"` // strings: an empty element must stay nil, not 0
		Loss       string `xml:"LossPriceCapped"`
		Congestion string `xml:"CongestionPriceCapped"`
		Flag       string `xml:"Flag"`
	} `xml:"DocBody>HourlyPriceComponents"`
}

// DAPriceHour is one hour of the day-ahead Ontario zonal energy price, in $/MWh.
type DAPriceHour struct {
	HourEnding       int       // 1..24 as published (hour 1 covers 00:00-01:00 EST)
	IntervalStartUTC time.Time // start of the hour, in UTC
	ZonalPrice       *float64
	LossPrice        *float64
	CongestionPrice  *float64
	Flag             string
}

// ParseDAHourlyOntarioZonalPrice decodes a DAHourlyOntarioZonalPrice daily report.
func ParseDAHourlyOntarioZonalPrice(data []byte) ([]DAPriceHour, error) {
	var doc daPriceDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("ieso: decode DAHourlyOntarioZonalPrice: %w", err)
	}
	day, err := time.ParseInLocation("2006-01-02", doc.Date, EST)
	if err != nil {
		return nil, fmt.Errorf("ieso: bad report date %q (schema drift?): %w", doc.Date, err)
	}
	if len(doc.Hours) == 0 {
		return nil, fmt.Errorf("ieso: DAHourlyOntarioZonalPrice has no hourly rows (schema drift?)")
	}
	rows := make([]DAPriceHour, 0, len(doc.Hours))
	for _, h := range doc.Hours {
		if h.Hour < 1 || h.Hour > 24 {
			return nil, fmt.Errorf("ieso: invalid pricing hour %d", h.Hour)
		}
		price, err := optionalFloat(h.Price)
		if err != nil {
			return nil, fmt.Errorf("ieso: hour %d bad ZonalPrice %q: %w", h.Hour, h.Price, err)
		}
		loss, err := optionalFloat(h.Loss)
		if err != nil {
			return nil, fmt.Errorf("ieso: hour %d bad LossPriceCapped %q: %w", h.Hour, h.Loss, err)
		}
		cong, err := optionalFloat(h.Congestion)
		if err != nil {
			return nil, fmt.Errorf("ieso: hour %d bad CongestionPriceCapped %q: %w", h.Hour, h.Congestion, err)
		}
		rows = append(rows, DAPriceHour{
			HourEnding:       h.Hour,
			IntervalStartUTC: day.Add(time.Duration(h.Hour-1) * time.Hour).UTC(),
			ZonalPrice:       price,
			LossPrice:        loss,
			CongestionPrice:  cong,
			Flag:             strings.TrimSpace(h.Flag),
		})
	}
	return rows, nil
}
