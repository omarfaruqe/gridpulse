package eccc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/retry"
)

// DefaultBaseURL is the MSC GeoMet OGC API.
const DefaultBaseURL = "https://api.weather.gc.ca"

// Source is the bronze partition name for this feed.
const Source = "eccc_climate_hourly"

const maxBody = 5 << 20 // 5 MiB safety limit

// ErrNoData means ECCC has no observations yet for that station and UTC day.
var ErrNoData = errors.New("eccc: no observations for station and day")

// Putter is the part of the bronze store the extractor needs.
type Putter interface {
	Put(ctx context.Context, source string, date time.Time, filename, contentType string, data []byte) (string, error)
}

// Extractor downloads hourly observations into bronze, one file per station
// per UTC day.
type Extractor struct {
	BaseURL   string
	Client    *http.Client
	Store     Putter
	Stations  []Station
	Attempts  int
	BaseDelay time.Duration
	Pause     time.Duration // delay between requests
}

// New returns an Extractor with production defaults and the embedded stations.
func New(store Putter) (*Extractor, error) {
	stations, err := Stations()
	if err != nil {
		return nil, err
	}
	return &Extractor{
		BaseURL:   DefaultBaseURL,
		Client:    &http.Client{Timeout: 60 * time.Second},
		Store:     store,
		Stations:  stations,
		Attempts:  4,
		BaseDelay: 2 * time.Second,
		Pause:     200 * time.Millisecond,
	}, nil
}

// Result summarises one stored station-day.
type Result struct {
	Key       string
	ClimateID string
	Day       string
	Rows      int
}

// FetchStationDay downloads one station's observations for one UTC day. The
// bronze key depends only on the station and the day, so re-running replaces
// the object with ECCC's latest data (idempotent). A day with no observations
// is not stored; it returns ErrNoData.
func (e *Extractor) FetchStationDay(ctx context.Context, st Station, day time.Time) (Result, error) {
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)

	q := url.Values{}
	q.Set("f", "json")
	q.Set("CLIMATE_IDENTIFIER", st.ClimateID)
	q.Set("UTC_YEAR", strconv.Itoa(d.Year()))
	q.Set("UTC_MONTH", strconv.Itoa(int(d.Month())))
	q.Set("UTC_DAY", strconv.Itoa(d.Day()))
	q.Set("limit", "100")
	u := fmt.Sprintf("%s/collections/climate-hourly/items?%s", e.BaseURL, q.Encode())

	body, err := e.download(ctx, u)
	if err != nil {
		return Result{}, err
	}
	obs, err := ParseHourly(body) // validate before storing
	if err != nil {
		return Result{}, err
	}
	if len(obs) == 0 {
		return Result{}, fmt.Errorf("%w: station %s on %s", ErrNoData, st.ClimateID, d.Format("2006-01-02"))
	}
	for _, o := range obs {
		if o.ClimateID != st.ClimateID || !o.TimeUTC.Truncate(24*time.Hour).Equal(d) {
			return Result{}, fmt.Errorf("eccc: response row (%s at %s) does not match request (%s on %s)",
				o.ClimateID, o.TimeUTC.Format(time.RFC3339), st.ClimateID, d.Format("2006-01-02"))
		}
	}
	name := fmt.Sprintf("%s_%s.json", st.ClimateID, d.Format("2006-01-02"))
	key, err := e.Store.Put(ctx, Source, d, name, "application/geo+json", body)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, ClimateID: st.ClimateID, Day: d.Format("2006-01-02"), Rows: len(obs)}, nil
}

// FetchDay fetches every configured station for one UTC day. Stations with no
// data yet are listed in missing. One failing station does not stop the rest;
// errors are joined and returned at the end.
func (e *Extractor) FetchDay(ctx context.Context, day time.Time) ([]Result, []string, error) {
	var stored []Result
	var missing []string
	var errs []error
	for _, st := range e.Stations {
		res, err := e.FetchStationDay(ctx, st, day)
		switch {
		case errors.Is(err, ErrNoData):
			missing = append(missing, st.ClimateID)
		case err != nil:
			errs = append(errs, fmt.Errorf("station %s: %w", st.ClimateID, err))
		default:
			stored = append(stored, res)
		}
		if err := e.pause(ctx); err != nil {
			errs = append(errs, err)
			break
		}
	}
	return stored, missing, errors.Join(errs...)
}

// Backfill fetches every station for every UTC day from..to inclusive. Missing
// entries look like "7025251@2026-11-06". Safe to re-run.
func (e *Extractor) Backfill(ctx context.Context, from, to time.Time) ([]Result, []string, error) {
	if to.Before(from) {
		return nil, nil, fmt.Errorf("eccc: backfill end %s is before start %s", to.Format("2006-01-02"), from.Format("2006-01-02"))
	}
	var stored []Result
	var missing []string
	var errs []error
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		res, miss, err := e.FetchDay(ctx, d)
		stored = append(stored, res...)
		for _, id := range miss {
			missing = append(missing, id+"@"+d.Format("2006-01-02"))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d.Format("2006-01-02"), err))
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
	}
	return stored, missing, errors.Join(errs...)
}

func (e *Extractor) pause(ctx context.Context) error {
	if e.Pause <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(e.Pause):
		return nil
	}
}

// download fetches a URL with retries. A client error (except 429) is
// permanent and returned without retrying.
func (e *Extractor) download(ctx context.Context, u string) ([]byte, error) {
	var body []byte
	var permanent error
	err := retry.Do(ctx, e.Attempts, e.BaseDelay, func() error {
		b, status, err := e.get(ctx, u)
		if err != nil {
			return err
		}
		switch {
		case status == http.StatusOK:
			body = b
			return nil
		case status >= 400 && status < 500 && status != http.StatusTooManyRequests:
			permanent = fmt.Errorf("eccc: HTTP %d for %s", status, u)
			return nil
		default:
			return fmt.Errorf("unexpected status %d", status)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("eccc: fetch %s: %w", u, err)
	}
	if permanent != nil {
		return nil, permanent
	}
	return body, nil
}

func (e *Extractor) get(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
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
