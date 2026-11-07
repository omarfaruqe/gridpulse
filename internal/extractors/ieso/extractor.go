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

// ErrNotPublished means IESO has no file for that report and day (HTTP 404).
var ErrNotPublished = errors.New("ieso: report not published")

// validators check a downloaded report before it is stored and return its row
// count. A report needs an entry here before it can be extracted.
var validators = map[string]func([]byte) (int, error){
	"GenOutputCapability": func(b []byte) (int, error) {
		rows, err := ParseGenOutputCapability(b)
		return len(rows), err
	},
}

// Source returns the bronze partition name for a report.
func Source(report string) string { return "ieso_" + strings.ToLower(report) }

// Putter is the part of the bronze store the extractor needs.
type Putter interface {
	Put(ctx context.Context, source string, date time.Time, filename, contentType string, data []byte) (string, error)
}

// Extractor downloads IESO daily reports into bronze.
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

// Result summarises one stored report day.
type Result struct {
	Key  string
	Day  string
	Rows int
}

// FetchDay downloads one report for one data day and stores it. The bronze key
// depends only on the report and the day, so re-running replaces the object
// with IESO's latest revision of that day (idempotent).
func (e *Extractor) FetchDay(ctx context.Context, report string, day time.Time) (Result, error) {
	validate, ok := validators[report]
	if !ok {
		return Result{}, fmt.Errorf("ieso: unsupported report %q", report)
	}
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	name := fmt.Sprintf("PUB_%s_%s.xml", report, d.Format("20060102"))
	url := fmt.Sprintf("%s/%s/%s", e.BaseURL, report, name)

	var body []byte
	var found bool
	err := retry.Do(ctx, e.Attempts, e.BaseDelay, func() error {
		var err error
		body, found, err = e.fetch(ctx, url)
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("ieso: fetch %s: %w", url, err)
	}
	if !found {
		return Result{}, fmt.Errorf("%w: %s", ErrNotPublished, url)
	}
	rows, err := validate(body) // validate before storing
	if err != nil {
		return Result{}, err
	}
	key, err := e.Store.Put(ctx, Source(report), d, name, "application/xml", body)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Day: d.Format("2006-01-02"), Rows: rows}, nil
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
