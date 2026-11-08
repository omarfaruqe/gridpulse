//go:build integration

package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/db/dbtest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newScheduler(t *testing.T, jobs ...Job) (*Scheduler, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.New(t)
	s, err := New(pool, quiet, jobs)
	if err != nil {
		t.Fatal(err)
	}
	return s, pool
}

func lastRun(t *testing.T, pool *pgxpool.Pool, job string) (status string, errText *string, finished bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
SELECT status, error, finished_at IS NOT NULL FROM ops.job_run WHERE job = $1 ORDER BY id DESC LIMIT 1`, job).
		Scan(&status, &errText, &finished)
	if err != nil {
		t.Fatalf("no run recorded for %s: %v", job, err)
	}
	return
}

func TestRunOnceRecordsSuccess(t *testing.T) {
	s, pool := newScheduler(t, Job{Name: "good", Spec: "@hourly", Timeout: time.Second, Run: func(context.Context) error { return nil }})
	if err := s.RunOnce(context.Background(), "good"); err != nil {
		t.Fatal(err)
	}
	if status, errText, finished := lastRun(t, pool, "good"); status != "succeeded" || errText != nil || !finished {
		t.Errorf("run = %s err=%v finished=%v, want succeeded nil true", status, errText, finished)
	}
}

func TestRunOnceRecordsFailureAndPanicAndTimeout(t *testing.T) {
	boom := errors.New("upstream returned 500")
	s, pool := newScheduler(t,
		Job{Name: "fails", Spec: "@hourly", Timeout: time.Second, Run: func(context.Context) error { return boom }},
		Job{Name: "panics", Spec: "@hourly", Timeout: time.Second, Run: func(context.Context) error { panic("nil map") }},
		Job{Name: "hangs", Spec: "@hourly", Timeout: 50 * time.Millisecond, Run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
	)
	for name, want := range map[string]string{"fails": "upstream returned 500", "panics": "panic: nil map", "hangs": "deadline exceeded"} {
		if err := s.RunOnce(context.Background(), name); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		status, errText, finished := lastRun(t, pool, name)
		if status != "failed" || !finished || errText == nil || !strings.Contains(*errText, want) {
			t.Errorf("%s: status=%s finished=%v err=%v, want failed true containing %q", name, status, finished, errText, want)
		}
	}
}

func TestJobsNeverOverlap(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s, _ := newScheduler(t, Job{Name: "slow", Spec: "@hourly", Timeout: 5 * time.Second, Run: func(context.Context) error {
		close(started)
		<-release
		return nil
	}})
	done := make(chan error, 1)
	go func() { done <- s.RunOnce(context.Background(), "slow") }()
	<-started

	if err := s.RunOnce(context.Background(), "slow"); !errors.Is(err, ErrBusy) {
		t.Errorf("second run = %v, want ErrBusy", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUnknownJob(t *testing.T) {
	s, _ := newScheduler(t)
	if err := s.RunOnce(context.Background(), "nope"); err == nil {
		t.Error("expected an error for an unknown job")
	}
}

func TestStartClosesInterruptedRunsAndFiresJobs(t *testing.T) {
	ran := make(chan struct{}, 1)
	s, pool := newScheduler(t, Job{Name: "tick", Spec: "@every 1s", Timeout: time.Second, Run: func(context.Context) error {
		select {
		case ran <- struct{}{}:
		default:
		}
		return nil
	}})
	if _, err := pool.Exec(context.Background(), `INSERT INTO ops.job_run (job, status) VALUES ('crashed', 'running')`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(4 * time.Second):
		t.Fatal("job did not fire on its schedule")
	}
	stopCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}

	status, errText, finished := lastRun(t, pool, "crashed")
	if status != "failed" || !finished || errText == nil || !strings.Contains(*errText, "restarted") {
		t.Errorf("crashed run = %s finished=%v err=%v, want failed true 'restarted'", status, finished, errText)
	}
}
