package aeso

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

// DefaultBaseURL is the AESO public API gateway.
const DefaultBaseURL = "https://apimgw.aeso.ca/public"

const maxBody = 20 << 20 // 20 MiB safety limit

// ErrAuth means AESO rejected the API key or the subscription. Not retried.
var ErrAuth = errors.New("aeso: API key rejected")

type report struct {
	path     string
	validate func([]byte) (int, error)
}

var reports = map[string]report{
	"PoolPrice": {
		path: "poolprice-api/v1.1/price/poolPrice",
		validate: func(b []byte) (int, error) {
			rows, err := ParsePoolPrice(b)
			return len(rows), err
		},
	},
	"AlbertaInternalLoad": {
		path: "actualforecast-api/v1/load/albertaInternalLoad",
		validate: func(b []byte) (int, error) {
			rows, err := ParseAlbertaInternalLoad(b)
			return len(rows), err
		},
	},
}

// Source returns the bronze partition name for a report.
func Source(name string) string { return "aeso_" + strings.ToLower(name) }

// Putter is the part of the bronze store the extractor needs.
type Putter interface {
	Put(ctx context.Context, source string, date time.Time, filename, contentType string, data []byte) (string, error)
}

// Extractor downloads AESO reports into bronze, one file per Alberta data day.
type Extractor struct {
	BaseURL   string
	APIKey    string // sent in the API-KEY header; never logged
	Client    *http.Client
	Store     Putter
	Attempts  int
	BaseDelay time.Duration
	Pause     time.Duration // delay between days in a backfill
}

// New returns an Extractor with production defaults. It needs an API key.
func New(store Putter, apiKey string) (*Extractor, error) {
	if apiKey == "" {
		return nil, errors.New("aeso: GRIDPULSE_AESO_API_KEY is not set")
	}
	return &Extractor{
		BaseURL:   DefaultBaseURL,
		APIKey:    apiKey,
		Client:    &http.Client{Timeout: 60 * time.Second},
		Store:     store,
		Attempts:  4,
		BaseDelay: 2 * time.Second,
		Pause:     250 * time.Millisecond,
	}, nil
}

// Result summarises one stored report day.
type Result struct {
	Key  string
	Day  string
	Rows int
}

// FetchDay downloads one report for one Alberta data day and stores it. The
// bronze key depends only on the report and the day, so re-running replaces
// the object with the latest data for that day (idempotent).
func (e *Extractor) FetchDay(ctx context.Context, name string, day time.Time) (Result, error) {
	rep, ok := reports[name]
	if !ok {
		return Result{}, fmt.Errorf("aeso: unsupported report %q (available: PoolPrice, AlbertaInternalLoad)", name)
	}
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	date := d.Format("2006-01-02")
	url := fmt.Sprintf("%s/%s?startDate=%s&endDate=%s", e.BaseURL, rep.path, date, date)

	body, err := e.download(ctx, url)
	if err != nil {
		return Result{}, err
	}
	rows, err := rep.validate(body) // validate before storing
	if err != nil {
		return Result{}, err
	}
	key, err := e.Store.Put(ctx, Source(name), d, fmt.Sprintf("%s_%s.json", name, date), "application/json", body)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Day: date, Rows: rows}, nil
}

// Backfill fetches every day from..to inclusive. Any error stops the run.
// Safe to re-run: each day maps to one bronze key.
func (e *Extractor) Backfill(ctx context.Context, name string, from, to time.Time) ([]Result, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("aeso: backfill end %s is before start %s", to.Format("2006-01-02"), from.Format("2006-01-02"))
	}
	var stored []Result
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		res, err := e.FetchDay(ctx, name, d)
		if err != nil {
			return stored, fmt.Errorf("aeso: backfill %s: %w", d.Format("2006-01-02"), err)
		}
		stored = append(stored, res)
		if e.Pause > 0 {
			select {
			case <-ctx.Done():
				return stored, ctx.Err()
			case <-time.After(e.Pause):
			}
		}
	}
	return stored, nil
}

// download fetches a URL with retries. A rejected key or other client error
// (except 429) is permanent and is returned without retrying.
func (e *Extractor) download(ctx context.Context, url string) ([]byte, error) {
	var body []byte
	var permanent error
	err := retry.Do(ctx, e.Attempts, e.BaseDelay, func() error {
		b, status, err := e.get(ctx, url)
		if err != nil {
			return err
		}
		switch {
		case status == http.StatusOK:
			body = b
			return nil
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			permanent = fmt.Errorf("%w (HTTP %d)", ErrAuth, status)
			return nil
		case status >= 400 && status < 500 && status != http.StatusTooManyRequests:
			permanent = fmt.Errorf("aeso: HTTP %d for %s", status, url)
			return nil
		default:
			return fmt.Errorf("unexpected status %d", status)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("aeso: fetch %s: %w", url, err)
	}
	if permanent != nil {
		return nil, permanent
	}
	return body, nil
}

func (e *Extractor) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("API-KEY", e.APIKey)
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, 0, err
	}
	return body, resp.StatusCode, nil
}
