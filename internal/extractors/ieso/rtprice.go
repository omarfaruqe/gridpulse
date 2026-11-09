package ieso

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RTPriceReport is the IESO report with the real-time 5-minute Ontario zonal
// energy price. It is published as one file per delivery hour.
const RTPriceReport = "RealtimeOntarioZonalPrice"

// IsHourly reports whether a report is published as one file per delivery hour.
func IsHourly(report string) bool { return report == RTPriceReport }

const rtIntervals = 12 // five-minute intervals in an hour

type rtPriceDoc struct {
	Created string `xml:"DocHeader>CreatedAt"`
	Date    string `xml:"DocBody>DeliveryDate"`
	Hour    int    `xml:"DocBody>DeliveryHour"`
	Prices  []struct {
		Interval   int    `xml:"Interval"`
		Flag       string `xml:"Flag"`
		Price      string `xml:"LmpCap"` // strings: an empty element must stay nil, not 0
		Loss       string `xml:"LossPriceCap"`
		Congestion string `xml:"CongPriceCap"`
	} `xml:"DocBody>ZonalPrice"`
}

// RTPriceInterval is one published five-minute interval, in $/MWh. The report
// names the fields LmpCap, LossPriceCap and CongPriceCap; they are stored as
// published.
type RTPriceInterval struct {
	Interval         int       // 1..12 within the delivery hour
	IntervalStartUTC time.Time // start of the five-minute interval, in UTC
	ZonalPrice       float64
	LossPrice        *float64
	CongestionPrice  *float64
	Flag             string
}

// RTPriceHour is one delivery-hour file. Intervals not yet published (empty
// elements in the file) are left out, never turned into zeros.
type RTPriceHour struct {
	Day        time.Time // the delivery date at 00:00 EST
	HourEnding int       // 1..24 as published (hour 1 covers 00:00-01:00 EST)
	Created    time.Time // the file's CreatedAt, in UTC; identifies the snapshot
	Intervals  []RTPriceInterval
}

// ParseRealtimeOntarioZonalPrice decodes one hourly RealtimeOntarioZonalPrice file.
func ParseRealtimeOntarioZonalPrice(data []byte) (RTPriceHour, error) {
	var doc rtPriceDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return RTPriceHour{}, fmt.Errorf("ieso: decode %s: %w", RTPriceReport, err)
	}
	day, err := time.ParseInLocation("2006-01-02", doc.Date, EST)
	if err != nil {
		return RTPriceHour{}, fmt.Errorf("ieso: bad delivery date %q (schema drift?): %w", doc.Date, err)
	}
	if doc.Hour < 1 || doc.Hour > 24 {
		return RTPriceHour{}, fmt.Errorf("ieso: invalid delivery hour %d", doc.Hour)
	}
	created, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(doc.Created), EST)
	if err != nil {
		return RTPriceHour{}, fmt.Errorf("ieso: bad CreatedAt %q (schema drift?): %w", doc.Created, err)
	}
	if len(doc.Prices) != rtIntervals {
		return RTPriceHour{}, fmt.Errorf("ieso: %s has %d intervals, want %d (schema drift?)", RTPriceReport, len(doc.Prices), rtIntervals)
	}

	out := RTPriceHour{Day: day, HourEnding: doc.Hour, Created: created.UTC()}
	seen := map[int]bool{}
	for _, p := range doc.Prices {
		if p.Interval < 1 || p.Interval > rtIntervals || seen[p.Interval] {
			return RTPriceHour{}, fmt.Errorf("ieso: invalid or repeated interval %d", p.Interval)
		}
		seen[p.Interval] = true

		price, err := optionalFloat(p.Price)
		if err != nil {
			return RTPriceHour{}, fmt.Errorf("ieso: interval %d bad LmpCap %q: %w", p.Interval, p.Price, err)
		}
		loss, err := optionalFloat(p.Loss)
		if err != nil {
			return RTPriceHour{}, fmt.Errorf("ieso: interval %d bad LossPriceCap %q: %w", p.Interval, p.Loss, err)
		}
		cong, err := optionalFloat(p.Congestion)
		if err != nil {
			return RTPriceHour{}, fmt.Errorf("ieso: interval %d bad CongPriceCap %q: %w", p.Interval, p.Congestion, err)
		}
		if price == nil {
			if loss != nil || cong != nil || strings.TrimSpace(p.Flag) != "" {
				return RTPriceHour{}, fmt.Errorf("ieso: interval %d has components but no price (schema drift?)", p.Interval)
			}
			continue // not published yet
		}
		start := day.Add(time.Duration(doc.Hour-1)*time.Hour + time.Duration(p.Interval-1)*5*time.Minute)
		out.Intervals = append(out.Intervals, RTPriceInterval{
			Interval:         p.Interval,
			IntervalStartUTC: start.UTC(),
			ZonalPrice:       *price,
			LossPrice:        loss,
			CongestionPrice:  cong,
			Flag:             strings.TrimSpace(p.Flag),
		})
	}
	return out, nil
}

// FetchHour downloads the file of one delivery hour and stores it. The file is
// rewritten as intervals are published, so the bronze object is named after its
// CreatedAt stamp: the same upstream version always maps to the same key and a
// newer version gets a new object. A file with no published interval yet is
// reported as not published and is not stored.
func (e *Extractor) FetchHour(ctx context.Context, day time.Time, hourEnding int) (Result, error) {
	if hourEnding < 1 || hourEnding > 24 {
		return Result{}, fmt.Errorf("ieso: invalid delivery hour %d", hourEnding)
	}
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	stamp := fmt.Sprintf("%s%02d", d.Format("20060102"), hourEnding)

	body, err := e.download(ctx, fmt.Sprintf("%s/%s/PUB_%s_%s.xml", e.BaseURL, RTPriceReport, RTPriceReport, stamp))
	if err != nil {
		return Result{}, err
	}
	h, err := ParseRealtimeOntarioZonalPrice(body) // validate before storing
	if err != nil {
		return Result{}, err
	}
	if h.Day.Format("2006-01-02") != d.Format("2006-01-02") || h.HourEnding != hourEnding {
		return Result{}, fmt.Errorf("ieso: asked for %s but the file holds %s hour %d", stamp, h.Day.Format("2006-01-02"), h.HourEnding)
	}
	if len(h.Intervals) == 0 {
		return Result{}, fmt.Errorf("%w: %s has no priced interval yet", ErrNotPublished, stamp)
	}
	name := fmt.Sprintf("PUB_%s_%s_%s.xml", RTPriceReport, stamp, h.Created.Format("20060102T150405Z"))
	key, err := e.Store.Put(ctx, Source(RTPriceReport), d, name, "application/xml", body)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Period: stamp, Rows: len(h.Intervals)}, nil
}

// BackfillHours fetches every delivery hour of every day from..to inclusive.
// Hours IESO has not published (or no longer keeps: about 90 days) are
// collected in missing; any other error stops the run. Safe to re-run.
func (e *Extractor) BackfillHours(ctx context.Context, from, to time.Time) (stored []Result, missing []string, err error) {
	if to.Before(from) {
		return nil, nil, fmt.Errorf("ieso: backfill end %s is before start %s", to.Format("2006-01-02"), from.Format("2006-01-02"))
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		for hour := 1; hour <= 24; hour++ {
			res, err := e.FetchHour(ctx, d, hour)
			switch {
			case errors.Is(err, ErrNotPublished):
				missing = append(missing, fmt.Sprintf("%s%02d", d.Format("20060102"), hour))
			case err != nil:
				return stored, missing, err
			default:
				stored = append(stored, res)
			}
		}
	}
	return stored, missing, nil
}
