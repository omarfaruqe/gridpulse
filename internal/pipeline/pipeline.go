// Package pipeline wires the extractors, loader, transform and freshness check
// into scheduled jobs. Each source job extracts recent data and loads exactly
// the bronze files it just wrote; a separate job rebuilds silver and gold when
// something new arrived.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
	"github.com/omarfaruqe/gridpulse/internal/extractors/aeso"
	"github.com/omarfaruqe/gridpulse/internal/extractors/eccc"
	"github.com/omarfaruqe/gridpulse/internal/extractors/hydroquebec"
	"github.com/omarfaruqe/gridpulse/internal/extractors/ieso"
	"github.com/omarfaruqe/gridpulse/internal/extractors/statcan"
	"github.com/omarfaruqe/gridpulse/internal/freshness"
	"github.com/omarfaruqe/gridpulse/internal/loader"
	"github.com/omarfaruqe/gridpulse/internal/scheduler"
	"github.com/omarfaruqe/gridpulse/internal/transform"
)

// Config holds everything the jobs need.
type Config struct {
	Store     *bronze.Store
	Loader    *loader.Loader
	Transform *transform.Runner
	Pool      *pgxpool.Pool
	AESOKey   string // empty: the AESO jobs are left out
	// DisabledJobs lists jobs to leave out by name, for a host that cannot
	// reach one source (Hydro-Québec refuses connections from outside Canada).
	DisabledJobs []string
	Log          *slog.Logger
	Now          func() time.Time // defaults to time.Now
}

// Pipeline is the configured set of jobs.
type Pipeline struct {
	Config

	dirty atomic.Bool // new rows were loaded since the last transform
}

// New returns a Pipeline whose first transform run always happens.
func New(c Config) *Pipeline {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	p := &Pipeline{Config: c}
	p.dirty.Store(true)
	return p
}

// days returns calendar days relative to now, as dates at midnight in loc.
// The extractors read only the year, month and day, so the zone chooses which
// operator-local day is meant.
func days(now time.Time, loc *time.Location, offsets ...int) []time.Time {
	local := now.In(loc)
	out := make([]time.Time, len(offsets))
	for i, o := range offsets {
		out[i] = time.Date(local.Year(), local.Month(), local.Day()+o, 0, 0, 0, 0, loc)
	}
	return out
}

// Jobs returns the schedule without the jobs named in DisabledJobs. An unknown
// name is an error, so a typo cannot silently leave a job running.
func (p *Pipeline) Jobs() ([]scheduler.Job, error) {
	all, err := p.allJobs()
	if err != nil {
		return nil, err
	}
	if len(p.DisabledJobs) == 0 {
		return all, nil
	}
	off := map[string]bool{}
	for _, n := range p.DisabledJobs {
		off[n] = true
	}
	var jobs []scheduler.Job
	for _, j := range all {
		if off[j.Name] {
			delete(off, j.Name)
			p.Log.Warn("job disabled by configuration", "job", j.Name)
			continue
		}
		jobs = append(jobs, j)
	}
	for n := range off {
		return nil, fmt.Errorf("pipeline: GRIDPULSE_DISABLE_JOBS names unknown job %q", n)
	}
	return jobs, nil
}

// allJobs returns the full schedule. Cron expressions are in UTC.
func (p *Pipeline) allJobs() ([]scheduler.Job, error) {
	hq := hydroquebec.New(p.Store)
	io := ieso.New(p.Store)
	ec, err := eccc.New(p.Store)
	if err != nil {
		return nil, err
	}

	jobs := []scheduler.Job{
		{Name: "hydroquebec", Spec: "*/15 * * * *", Timeout: 2 * time.Minute, Run: func(ctx context.Context) error {
			res, err := hq.Run(ctx)
			if err != nil {
				return err
			}
			return p.load(ctx, "hydroquebec", res.Key)
		}},
		{Name: "ieso-demand", Spec: "20 * * * *", Timeout: 3 * time.Minute, Run: func(ctx context.Context) error {
			return p.iesoYearly(ctx, io, "Demand", "ieso-demand")
		}},
		{Name: "ieso-fuel", Spec: "35 * * * *", Timeout: 5 * time.Minute, Run: func(ctx context.Context) error {
			return p.iesoYearly(ctx, io, "GenOutputbyFuelHourly", "ieso-fuel")
		}},
		{Name: "ieso-generation", Spec: "25 * * * *", Timeout: 3 * time.Minute,
			Run: p.iesoDaily(io, "GenOutputCapability", "ieso-generation", -1, 0)},
		// Day-ahead prices are published for tomorrow in the afternoon; "tomorrow" is
		// usually not there yet, which is not an error.
		{Name: "ieso-price", Spec: "30 */2 * * *", Timeout: 3 * time.Minute,
			Run: p.iesoDaily(io, "DAHourlyOntarioZonalPrice", "ieso-price", -1, 0, 1)},
		// Real-time prices come as one file per delivery hour, rewritten every five
		// minutes while the hour runs. The frequent job re-reads the last few hours;
		// the hourly one re-reads two days so a gap (for example after the machine
		// slept) closes by itself.
		{Name: "ieso-rtprice", Spec: "2-57/5 * * * *", Timeout: 2 * time.Minute, Run: p.iesoRealtime(io, 3)},
		{Name: "ieso-rtprice-catchup", Spec: "17 * * * *", Timeout: 5 * time.Minute, Run: p.iesoRealtime(io, 48)},
		{Name: "eccc", Spec: "40 */6 * * *", Timeout: 10 * time.Minute, Run: func(ctx context.Context) error {
			return p.ecccRecent(ctx, ec)
		}},
		// StatCan publishes the table about monthly; the download is content-addressed,
		// so checking daily stores a new object only when the table changed.
		{Name: "statcan", Spec: "15 12 * * *", Timeout: 5 * time.Minute, Run: func(ctx context.Context) error {
			res, err := statcan.New(p.Store).FetchGeneration(ctx)
			if err != nil {
				return err
			}
			return p.load(ctx, "statcan-generation", res.Key)
		}},
		{Name: "transform", Spec: "*/10 * * * *", Timeout: 5 * time.Minute, Run: p.transformJob},
		{Name: "freshness", Spec: "3,18,33,48 * * * *", Timeout: time.Minute, Run: p.freshnessJob},
	}

	if p.AESOKey == "" {
		p.Log.Warn("GRIDPULSE_AESO_API_KEY is not set: AESO jobs are not scheduled")
		return jobs, nil
	}
	ae, err := aeso.New(p.Store, p.AESOKey)
	if err != nil {
		return nil, err
	}
	return append(jobs,
		scheduler.Job{Name: "aeso-price", Spec: "10 * * * *", Timeout: 3 * time.Minute,
			Run: p.aesoDaily(ae, "PoolPrice", "aeso-price")},
		scheduler.Job{Name: "aeso-load", Spec: "12 * * * *", Timeout: 3 * time.Minute,
			Run: p.aesoDaily(ae, "AlbertaInternalLoad", "aeso-load")},
	), nil
}

// load loads one bronze file and notes that silver and gold are out of date.
func (p *Pipeline) load(ctx context.Context, name, key string) error {
	res, err := p.Loader.Load(ctx, name, key, false)
	if err != nil {
		return err
	}
	if !res.Skipped {
		p.dirty.Store(true)
	}
	return nil
}

// iesoYearly fetches this year's file of a yearly report, and last year's during
// the first days of January so the final hours of the old year are not left
// half-published.
func (p *Pipeline) iesoYearly(ctx context.Context, ex *ieso.Extractor, report, loadName string) error {
	now := p.Now().In(ieso.EST)
	years := []int{now.Year()}
	if now.Month() == time.January && now.Day() <= 7 {
		years = append(years, now.Year()-1)
	}
	var errs []error
	for _, y := range years {
		res, err := ex.FetchYear(ctx, report, y)
		if errors.Is(err, ieso.ErrNotPublished) {
			p.Log.Info("report not published", "report", report, "year", y)
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %d: %w", report, y, err))
			continue
		}
		errs = append(errs, p.load(ctx, loadName, res.Key))
	}
	return errors.Join(errs...)
}

func (p *Pipeline) iesoDaily(ex *ieso.Extractor, report, loadName string, offsets ...int) func(context.Context) error {
	return func(ctx context.Context) error {
		var errs []error
		for _, day := range days(p.Now(), ieso.EST, offsets...) {
			res, err := ex.FetchDay(ctx, report, day)
			if errors.Is(err, ieso.ErrNotPublished) {
				p.Log.Info("report not published yet", "report", report, "day", day.Format("2006-01-02"))
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", report, day.Format("2006-01-02"), err))
				continue
			}
			errs = append(errs, p.load(ctx, loadName, res.Key))
		}
		return errors.Join(errs...)
	}
}

// iesoRealtime fetches the hourly real-time price files of the last n delivery
// hours, newest first. Hours without a priced interval yet are not an error.
func (p *Pipeline) iesoRealtime(ex *ieso.Extractor, n int) func(context.Context) error {
	return func(ctx context.Context) error {
		var errs []error
		for k := 0; k < n; k++ {
			t := p.Now().Add(-time.Duration(k) * time.Hour).In(ieso.EST)
			hour := t.Hour() + 1 // delivery hours are hour ending, 1..24
			res, err := ex.FetchHour(ctx, t, hour)
			if errors.Is(err, ieso.ErrNotPublished) {
				p.Log.Debug("real-time price not published", "day", t.Format("2006-01-02"), "hour", hour)
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s hour %d: %w", ieso.RTPriceReport, t.Format("2006-01-02"), hour, err))
				continue
			}
			errs = append(errs, p.load(ctx, "ieso-rtprice", res.Key))
		}
		return errors.Join(errs...)
	}
}

func (p *Pipeline) aesoDaily(ex *aeso.Extractor, report, loadName string) func(context.Context) error {
	return func(ctx context.Context) error {
		var errs []error
		for _, day := range days(p.Now(), aeso.Mountain, -1, 0) {
			res, err := ex.FetchDay(ctx, report, day)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", report, day.Format("2006-01-02"), err))
				continue
			}
			errs = append(errs, p.load(ctx, loadName, res.Key))
		}
		return errors.Join(errs...)
	}
}

// ecccRecent refetches the last three UTC days: the archive trails real time by
// about a day and ECCC revises recent observations, so recent days are re-read
// and the loader's content hash decides whether anything changed.
func (p *Pipeline) ecccRecent(ctx context.Context, ex *eccc.Extractor) error {
	var errs []error
	for _, day := range days(p.Now(), time.UTC, -2, -1, 0) {
		stored, missing, err := ex.FetchDay(ctx, day)
		if err != nil {
			errs = append(errs, fmt.Errorf("eccc %s: %w", day.Format("2006-01-02"), err))
		}
		if len(missing) > 0 {
			p.Log.Info("no observations yet", "day", day.Format("2006-01-02"), "stations", missing)
		}
		for _, res := range stored {
			errs = append(errs, p.load(ctx, "eccc", res.Key))
		}
	}
	return errors.Join(errs...)
}

// transformJob rebuilds silver and gold when new rows arrived, then runs the
// data quality tests. Failing tests fail the job (so the run is recorded as
// failed) but the rebuilt layers stay in place.
func (p *Pipeline) transformJob(ctx context.Context) error {
	if !p.dirty.Swap(false) {
		return nil
	}
	if err := p.Transform.Build(ctx); err != nil {
		p.dirty.Store(true) // try again next time
		return err
	}
	results, err := p.Transform.Test(ctx)
	if err != nil {
		return err
	}
	if err := p.Transform.Record(ctx, results); err != nil {
		p.Log.Warn("could not record test results", "error", err)
	}
	var failed []string
	for _, t := range results {
		if !t.Passed() {
			p.Log.Error("data quality test failed", "test", t.Name, "violations", t.Violations, "sample", t.Sample)
			failed = append(failed, fmt.Sprintf("%s (%d)", t.Name, t.Violations))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("data quality tests failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// freshnessJob fails when any series is older than its limit.
func (p *Pipeline) freshnessJob(ctx context.Context) error {
	statuses, err := freshness.Check(ctx, p.Pool, p.Now())
	if err != nil {
		return err
	}
	if err := freshness.Record(ctx, p.Pool, p.Now(), statuses); err != nil {
		p.Log.Warn("could not record freshness", "error", err)
	}
	var stale []string
	for _, s := range statuses {
		if s.Stale {
			p.Log.Warn("series is stale", "series", s.Series, "latest", s.LatestHour, "lag", s.Lag.Round(time.Minute), "limit", s.MaxLag)
			stale = append(stale, s.Series)
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("%d series stale: %s", len(stale), strings.Join(stale, ", "))
	}
	return nil
}
