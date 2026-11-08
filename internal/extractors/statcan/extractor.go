package statcan

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/retry"
)

// DefaultBaseURL is where Statistics Canada serves full-table CSV downloads.
const DefaultBaseURL = "https://www150.statcan.gc.ca/n1/tbl/csv"

// Source is the bronze partition and load_log source for the table.
const Source = "statcan_25100015"

const (
	productID = "25100015"
	maxZip    = 100 << 20 // 100 MiB safety limits
	maxCSV    = 500 << 20
)

// Putter is the part of the bronze store the extractor needs.
type Putter interface {
	Put(ctx context.Context, source string, date time.Time, filename, contentType string, data []byte) (string, error)
}

// Extractor downloads the table into bronze.
type Extractor struct {
	BaseURL   string
	Client    *http.Client
	Store     Putter
	Attempts  int
	BaseDelay time.Duration
	Now       func() time.Time
}

// New returns an Extractor with production defaults.
func New(store Putter) *Extractor {
	return &Extractor{
		BaseURL:   DefaultBaseURL,
		Client:    &http.Client{Timeout: 3 * time.Minute},
		Store:     store,
		Attempts:  4,
		BaseDelay: 2 * time.Second,
		Now:       time.Now,
	}
}

// Result summarises one stored download.
type Result struct {
	Key  string
	Rows int
}

// FetchGeneration downloads the table, validates it and stores the CSV. The
// object is named after a hash of its content, so downloading an unchanged
// table rewrites the same key (idempotent) and a revised table gets a new one.
func (e *Extractor) FetchGeneration(ctx context.Context) (Result, error) {
	url := fmt.Sprintf("%s/%s-eng.zip", e.BaseURL, productID)
	archive, err := e.download(ctx, url)
	if err != nil {
		return Result{}, err
	}
	data, err := extractCSV(archive, productID+".csv")
	if err != nil {
		return Result{}, err
	}
	rows, err := ParseGeneration(data) // validate before storing
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(data)
	name := fmt.Sprintf("%s_%s.csv", productID, hex.EncodeToString(sum[:])[:12])
	key, err := e.Store.Put(ctx, Source, e.Now().UTC(), name, "text/csv", data)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Rows: len(rows)}, nil
}

// extractCSV returns the named file from a zip archive held in memory.
func extractCSV(archive []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("statcan: open zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if f.UncompressedSize64 > maxCSV {
			return nil, fmt.Errorf("statcan: %s is %d bytes uncompressed, over the %d limit", name, f.UncompressedSize64, maxCSV)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("statcan: open %s: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxCSV+1))
		if err != nil {
			return nil, fmt.Errorf("statcan: read %s: %w", name, err)
		}
		if len(data) > maxCSV {
			return nil, fmt.Errorf("statcan: %s is over the %d limit", name, maxCSV)
		}
		return data, nil
	}
	return nil, fmt.Errorf("statcan: %s not found in zip (schema drift?)", name)
}

func (e *Extractor) download(ctx context.Context, url string) ([]byte, error) {
	var body []byte
	err := retry.Do(ctx, e.Attempts, e.BaseDelay, func() error {
		var err error
		body, err = e.get(ctx, url)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("statcan: fetch %s: %w", url, err)
	}
	return body, nil
}

func (e *Extractor) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxZip+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxZip {
		return nil, errors.New("response larger than the safety limit")
	}
	return body, nil
}
