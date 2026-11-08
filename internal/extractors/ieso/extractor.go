package ieso

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/retry"
)

// DefaultBaseURL is the root of IESO's public reports.
const DefaultBaseURL = "https://reports-public.ieso.ca/public"

const maxBody = 50 << 20 // 50 MiB safety limit

// ErrNotPublished means IESO has no file for that report and period (HTTP 404).
var ErrNotPublished = errors.New("ieso: report not published")

// validators check a downloaded daily report before it is stored and return
// its row count. A daily report needs an entry here before it can be extracted.
var validators = map[string]func([]byte) (int, error){
	"GenOutputCapability": func(b []byte) (int, error) {
		rows, err := ParseGenOutputCapability(b)
		return len(rows), err
	},
	"DAHourlyOntarioZonalPrice": func(b []byte) (int, error) {
		rows, err := ParseDAHourlyOntarioZonalPrice(b)
		return len(rows), err
	},
}

// yearlyValidators do the same for yearly reports, and also return the
// upstream "Created at" time used to name the bronze object.
var yearlyValidators = map[string]func([]byte) (int, time.Time, error){
	"Demand": func(b []byte) (int, time.Time, error) {
		rows, created, err := ParseDemand(b)
		return len(rows), created, err
	},
	"GenOutputbyFuelHourly": func(b []byte) (int, time.Time, error) {
		rows, created, err := ParseGenOutputbyFuelHourly(b)
		return len(rows), created, err
	},
}

// yearlyFormats lists the yearly reports that are not CSV.
var yearlyFormats = map[string]struct{ ext, contentType string }{
	"GenOutputbyFuelHourly": {"xml", "application/xml"},
}

// IsYearly reports whether a report is published as one file per year.
func IsYearly(report string) bool {
	_, ok := yearlyValidators[report]
	return ok
}

// Source returns the bronze partition name for a report.
func Source(report string) string { return "ieso_" + strings.ToLower(report) }

// Putter is the part of the bronze store the extractor needs.
type Putter interface {
	Put(ctx context.Context, source string, date time.Time, filename, contentType string, data []byte) (string, error)
}

// Extractor downloads IESO reports into bronze.
type Extractor struct {
	BaseURL   string
	Client    *http.Client
	Store     Putter
	Attempts  int
	BaseDelay time.Duration
}

// New returns an Extractor with production defaults.
func New(store Putter) *Extractor {
	return &Extractor{
		BaseURL:   DefaultBaseURL,
		Client:    &http.Client{Timeout: 60 * time.Second},
		Store:     store,
		Attempts:  4,
		BaseDelay: 2 * time.Second,
	}
}

// Result summarises one stored report file.
type Result struct {
	Key    string
	Period string // "2026-11-06" for daily reports, "2026" for yearly ones
	Rows   int
}

// FetchDay downloads one daily report and stores it. The bronze key depends
// only on the report and the day, so re-running replaces the object with
// IESO's latest revision of that day (idempotent).
func (e *Extractor) FetchDay(ctx context.Context, report string, day time.Time) (Result, error) {
	validate, ok := validators[report]
	if !ok {
		return Result{}, fmt.Errorf("ieso: unsupported daily report %q", report)
	}
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	name := fmt.Sprintf("PUB_%s_%s.xml", report, d.Format("20060102"))

	body, err := e.download(ctx, fmt.Sprintf("%s/%s/%s", e.BaseURL, report, name))
	if err != nil {
		return Result{}, err
	}
	rows, err := validate(body) // validate before storing
	if err != nil {
		return Result{}, err
	}
	key, err := e.Store.Put(ctx, Source(report), d, name, "application/xml", body)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Period: d.Format("2006-01-02"), Rows: rows}, nil
}

// FetchYear downloads a yearly CSV report. The file is rewritten upstream as
// the year progresses, so the bronze object is named after its "Created at"
// stamp: the same upstream version always maps to the same key (idempotent)
// and a new version gets a new object.
func (e *Extractor) FetchYear(ctx context.Context, report string, year int) (Result, error) {
	validate, ok := yearlyValidators[report]
	if !ok {
		return Result{}, fmt.Errorf("ieso: unsupported yearly report %q", report)
	}
	ext, contentType := "csv", "text/csv"
	if f, ok := yearlyFormats[report]; ok {
		ext, contentType = f.ext, f.contentType
	}
	body, err := e.download(ctx, fmt.Sprintf("%s/%s/PUB_%s_%d.%s", e.BaseURL, report, report, year, ext))
	if err != nil {
		return Result{}, err
	}
	rows, created, err := validate(body) // validate before storing
	if err != nil {
		return Result{}, err
	}
	name := fmt.Sprintf("PUB_%s_%d_%s.%s", report, year, created.UTC().Format("20060102T150405Z"), ext)
	key, err := e.Store.Put(ctx, Source(report), created, name, contentType, body)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Period: fmt.Sprint(year), Rows: rows}, nil
}

// Backfill fetches every day from..to inclusive. Days IESO has not published
// are collected in missing; any other error stops the run. Safe to re-run.
func (e *Extractor) Backfill(ctx context.Context, report string, from, to time.Time) (stored []Result, missing []string, err error) {
	if to.Before(from) {
		return nil, nil, fmt.Errorf("ieso: backfill end %s is before start %s", to.Format("2006-01-02"), from.Format("2006-01-02"))
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		res, err := e.FetchDay(ctx, report, d)
		switch {
		case errors.Is(err, ErrNotPublished):
			missing = append(missing, d.Format("2006-01-02"))
		case err != nil:
			return stored, missing, err
		default:
			stored = append(stored, res)
		}
	}
	return stored, missing, nil
}

// BackfillYears fetches every year from..to inclusive. Safe to re-run.
func (e *Extractor) BackfillYears(ctx context.Context, report string, from, to int) (stored []Result, missing []int, err error) {
	if to < from {
		return nil, nil, fmt.Errorf("ieso: backfill end year %d is before start year %d", to, from)
	}
	for y := from; y <= to; y++ {
		res, err := e.FetchYear(ctx, report, y)
		switch {
		case errors.Is(err, ErrNotPublished):
			missing = append(missing, y)
		case err != nil:
			return stored, missing, err
		default:
			stored = append(stored, res)
		}
	}
	return stored, missing, nil
}

// download fetches a URL with retries. A 404 is permanent and returns
// ErrNotPublished without retrying.
func (e *Extractor) download(ctx context.Context, url string) ([]byte, error) {
	var body []byte
	var found bool
	err := retry.Do(ctx, e.Attempts, e.BaseDelay, func() error {
		var err error
		body, found, err = e.fetch(ctx, url)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("ieso: fetch %s: %w", url, err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNotPublished, url)
	}
	return body, nil
}

func (e *Extractor) fetch(ctx context.Context, url string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil // permanent: do not retry
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}
