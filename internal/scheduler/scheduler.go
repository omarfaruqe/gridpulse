// Package scheduler runs named jobs on cron schedules and records every run
// in ops.job_run. It knows nothing about what the jobs do.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

// ErrBusy is returned when a job is asked to run while its previous run is unfinished.
var ErrBusy = errors.New("scheduler: job is still running")

// Job is one unit of scheduled work.
type Job struct {
	Name    string
	Spec    string // standard 5-field cron, or "@every 15m"; evaluated in UTC
	Timeout time.Duration
	Run     func(ctx context.Context) error
}

type entry struct {
	Job
	mu sync.Mutex // a job never overlaps itself
}

// Scheduler owns the jobs and the cron clock.
type Scheduler struct {
	pool  *pgxpool.Pool
	log   *slog.Logger
	jobs  map[string]*entry
	order []string
	cron  *cron.Cron
}

// New validates the jobs. Nothing runs until Start.
func New(pool *pgxpool.Pool, log *slog.Logger, jobs []Job) (*Scheduler, error) {
	s := &Scheduler{
		pool: pool,
		log:  log,
		jobs: make(map[string]*entry, len(jobs)),
		cron: cron.New(cron.WithLocation(time.UTC)),
	}
	for _, j := range jobs {
		switch {
		case j.Name == "":
			return nil, errors.New("scheduler: job without a name")
		case j.Run == nil:
			return nil, fmt.Errorf("scheduler: job %q has no Run function", j.Name)
		case j.Timeout <= 0:
			return nil, fmt.Errorf("scheduler: job %q needs a positive timeout", j.Name)
		}
		if _, dup := s.jobs[j.Name]; dup {
			return nil, fmt.Errorf("scheduler: duplicate job %q", j.Name)
		}
		if _, err := cron.ParseStandard(j.Spec); err != nil {
			return nil, fmt.Errorf("scheduler: job %q: bad schedule %q: %w", j.Name, j.Spec, err)
		}
		s.jobs[j.Name] = &entry{Job: j}
		s.order = append(s.order, j.Name)
	}
	return s, nil
}

// Names lists the jobs in registration order.
func (s *Scheduler) Names() []string { return append([]string(nil), s.order...) }

// Start closes out runs left "running" by a previous process, then begins
// firing jobs on schedule. ctx is the parent of every job run: cancelling it
// cancels running jobs.
func (s *Scheduler) Start(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `
UPDATE ops.job_run SET status = 'failed', finished_at = now(), error = 'interrupted: scheduler restarted'
WHERE status = 'running'`); err != nil {
		return fmt.Errorf("scheduler: close interrupted runs: %w", err)
	}
	for _, name := range s.order {
		e := s.jobs[name]
		if _, err := s.cron.AddFunc(e.Spec, func() {
			if err := s.run(ctx, e); errors.Is(err, ErrBusy) {
				s.log.Warn("job skipped: previous run still going", "job", e.Name)
			}
		}); err != nil {
			return fmt.Errorf("scheduler: schedule %q: %w", e.Name, err)
		}
	}
	s.cron.Start()
	return nil
}

// Stop stops firing new runs and waits for running jobs until ctx ends.
func (s *Scheduler) Stop(ctx context.Context) error {
	select {
	case <-s.cron.Stop().Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunOnce runs one job now, recorded like any scheduled run.
func (s *Scheduler) RunOnce(ctx context.Context, name string) error {
	e, ok := s.jobs[name]
	if !ok {
		return fmt.Errorf("scheduler: unknown job %q (available: %v)", name, s.order)
	}
	return s.run(ctx, e)
}

func (s *Scheduler) run(ctx context.Context, e *entry) error {
	if !e.mu.TryLock() {
		return ErrBusy
	}
	defer e.mu.Unlock()

	// Recording is best effort: a database outage must not stop an extract job.
	var id int64
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO ops.job_run (job, status) VALUES ($1, 'running') RETURNING id`, e.Name).Scan(&id); err != nil {
		s.log.Error("could not record job start", "job", e.Name, "error", err)
	}

	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	jobErr := execute(runCtx, e)

	if id != 0 {
		status, msg := "succeeded", (*string)(nil)
		if jobErr != nil {
			m := jobErr.Error()
			status, msg = "failed", &m
		}
		// Not tied to ctx: a shutdown must not lose the record of how the run ended.
		finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancelFinish()
		if _, err := s.pool.Exec(finishCtx,
			`UPDATE ops.job_run SET finished_at = now(), status = $2, error = $3 WHERE id = $1`,
			id, status, msg); err != nil {
			s.log.Error("could not record job end", "job", e.Name, "error", err)
		}
	}
	if jobErr != nil {
		s.log.Error("job failed", "job", e.Name, "took", time.Since(start).Round(time.Millisecond), "error", jobErr)
		return jobErr
	}
	s.log.Info("job succeeded", "job", e.Name, "took", time.Since(start).Round(time.Millisecond))
	return nil
}

func execute(ctx context.Context, e *entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return e.Run(ctx)
}
