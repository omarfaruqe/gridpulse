package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/omarfaruqe/gridpulse/internal/freshness"
)

// FreshnessEntry is how current one series is against its limit.
type FreshnessEntry struct {
	Series        string     `json:"series"`
	LatestHourUTC *time.Time `json:"latest_hour_utc"`
	LagSeconds    *int64     `json:"lag_seconds"` // negative when the data runs ahead of the clock
	MaxLagSeconds int64      `json:"max_lag_seconds"`
	Stale         bool       `json:"stale"`
	// WorstLag7dSeconds is the largest lag seen at any check in the last seven
	// days; null until checks have been recorded.
	WorstLag7dSeconds *int64 `json:"worst_lag_7d_seconds"`
}

// JobStatus summarises the runs of one scheduled job.
type JobStatus struct {
	Job            string  `db:"job" json:"job"`
	LastStatus     string  `db:"last_status" json:"last_status"`
	LastStartedAt  string  `db:"last_started_at" json:"last_started_at"`
	LastFinishedAt *string `db:"last_finished_at" json:"last_finished_at"`
	LastError      *string `db:"last_error" json:"last_error"`
	LastSuccessAt  *string `db:"last_success_at" json:"last_success_at"`
	Runs24h        int64   `db:"runs_24h" json:"runs_24h"`
	Failures24h    int64   `db:"failures_24h" json:"failures_24h"`
}

// QualityResult is the latest outcome of one data quality test.
type QualityResult struct {
	Test       string `db:"test" json:"test"`
	Violations int32  `db:"violations" json:"violations"`
	CheckedAt  string `db:"checked_at" json:"checked_at"`
}

// PipelineHealth is everything the pipeline-health view shows.
type PipelineHealth struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Summary     PipelineSummary  `json:"summary"`
	Freshness   []FreshnessEntry `json:"freshness"`
	Jobs        []JobStatus      `json:"jobs"`
	Quality     []QualityResult  `json:"quality"`
}

// PipelineSummary counts what is currently wrong.
type PipelineSummary struct {
	StaleSeries  int `json:"stale_series"`
	FailingJobs  int `json:"failing_jobs"`
	FailingTests int `json:"failing_tests"`
}

// Health gathers freshness, job and test state as of now.
func (a *API) Health(r *http.Request) (PipelineHealth, error) {
	ctx := r.Context()
	now := a.Now()

	statuses, err := freshness.Check(ctx, a.Pool, now)
	if err != nil {
		return PipelineHealth{}, err
	}
	worst, err := freshness.WorstLags(ctx, a.Pool, now)
	if err != nil {
		return PipelineHealth{}, err
	}
	out := PipelineHealth{GeneratedAt: now.UTC(), Freshness: make([]FreshnessEntry, 0, len(statuses))}
	for _, s := range statuses {
		e := FreshnessEntry{Series: s.Series, MaxLagSeconds: int64(s.MaxLag.Seconds()), Stale: s.Stale}
		if s.LatestHour != nil {
			latest := s.LatestHour.UTC() // the driver returns the host's local zone
			lag := int64(s.Lag.Seconds())
			e.LatestHourUTC, e.LagSeconds = &latest, &lag
		}
		if w, ok := worst[s.Series]; ok {
			e.WorstLag7dSeconds = &w
		}
		if s.Stale {
			out.Summary.StaleSeries++
		}
		out.Freshness = append(out.Freshness, e)
	}

	rows, err := a.Pool.Query(ctx, `
WITH latest_run AS (
    SELECT DISTINCT ON (job) job, status, started_at, finished_at, error
    FROM ops.job_run ORDER BY job, started_at DESC
), totals AS (
    SELECT job,
           max(finished_at) FILTER (WHERE status = 'succeeded')                              AS last_success_at,
           count(*) FILTER (WHERE started_at > $1::timestamptz - interval '24 hours')        AS runs_24h,
           count(*) FILTER (WHERE status = 'failed' AND started_at > $1::timestamptz - interval '24 hours') AS failures_24h
    FROM ops.job_run GROUP BY job
)
SELECT l.job, l.status AS last_status,
       `+iso("l.started_at")+` AS last_started_at, `+iso("l.finished_at")+` AS last_finished_at,
       left(l.error, 300) AS last_error, `+iso("a.last_success_at")+` AS last_success_at,
       a.runs_24h, a.failures_24h
FROM latest_run l JOIN totals a USING (job) ORDER BY l.job`, now)
	if err != nil {
		return PipelineHealth{}, err
	}
	if out.Jobs, err = pgx.CollectRows(rows, pgx.RowToStructByName[JobStatus]); err != nil {
		return PipelineHealth{}, err
	}
	if out.Jobs == nil {
		out.Jobs = []JobStatus{}
	}
	for _, j := range out.Jobs {
		if j.LastStatus == "failed" {
			out.Summary.FailingJobs++
		}
	}

	rows, err = a.Pool.Query(ctx, `
SELECT DISTINCT ON (test) test, violations, `+iso("run_at")+` AS checked_at
FROM ops.test_result ORDER BY test, run_at DESC`)
	if err != nil {
		return PipelineHealth{}, err
	}
	if out.Quality, err = pgx.CollectRows(rows, pgx.RowToStructByName[QualityResult]); err != nil {
		return PipelineHealth{}, err
	}
	if out.Quality == nil {
		out.Quality = []QualityResult{}
	}
	for _, q := range out.Quality {
		if q.Violations > 0 {
			out.Summary.FailingTests++
		}
	}
	return out, nil
}

func (a *API) pipeline(w http.ResponseWriter, r *http.Request) {
	h, err := a.Health(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}
