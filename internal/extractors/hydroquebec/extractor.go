package hydroquebec

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/retry"
)

// DefaultURL is the public real-time demand feed (about 48 hours, 15-minute slots).
const DefaultURL = "https://www.hydroquebec.com/data/documents-donnees/donnees-ouvertes/json/demande.json"

const maxBody = 5 << 20 // 5 MiB safety limit

// Putter is the part of the bronze store the extractor needs.
type Putter interface {
	Put(ctx context.Context, source string, date time.Time, filename, contentType string, data []byte) (string, error)
}

// Extractor downloads the feed and stores the raw bytes in bronze.
type Extractor struct {
	URL       string
	Client    *http.Client
	Store     Putter
	Attempts  int
	BaseDelay time.Duration
}

// New returns an Extractor with production defaults.
func New(store Putter) *Extractor {
	return &Extractor{
		URL:       DefaultURL,
		Client:    &http.Client{Timeout: 30 * time.Second},
		Store:     store,
		Attempts:  4,
		BaseDelay: 2 * time.Second,
	}
}

// Result summarises one run.
type Result struct {
	Key      string
	Records  int // slots in the feed
	Filled   int // slots that have a value
	Snapshot time.Time
}

// Run fetches the feed and writes it to bronze. The object name comes from the
// upstream Last-Modified header, so fetching the same version twice rewrites
// the same key (idempotent) and a new upstream version gets a new object.
func (e *Extractor) Run(ctx context.Context) (Result, error) {
	var body []byte
	var modified time.Time
	err := retry.Do(ctx, e.Attempts, e.BaseDelay, func() error {
		var err error
		body, modified, err = e.fetch(ctx)
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("hydroquebec: fetch: %w", err)
	}

	obs, err := Parse(body) // validate before storing
	if err != nil {
		return Result{}, err
	}
	filled := 0
	for _, o := range obs {
		if o.DemandMW != nil {
			filled++
		}
	}

	name := fmt.Sprintf("demand_%s.json", modified.UTC().Format("20060102T150405Z"))
	key, err := e.Store.Put(ctx, Source, modified, name, "application/json", body)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Records: len(obs), Filled: filled, Snapshot: modified.UTC()}, nil
}

func (e *Extractor) fetch(ctx context.Context) ([]byte, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, time.Time{}, err
	}
	modified, err := http.ParseTime(resp.Header.Get("Last-Modified"))
	if err != nil {
		modified = time.Now().UTC() // no upstream version marker: not idempotent
	}
	return body, modified, nil
}
