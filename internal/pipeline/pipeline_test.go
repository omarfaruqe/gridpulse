package pipeline

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/extractors/ieso"
	"github.com/omarfaruqe/gridpulse/internal/scheduler"
)

func TestDays(t *testing.T) {
	// 02:30 UTC on 1 Nov is still 21:30 EST on 31 Oct: the IESO day is 31 Oct.
	now := time.Date(2026, 11, 1, 2, 30, 0, 0, time.UTC)
	got := days(now, ieso.EST, -1, 0, 1)
	want := []string{"2026-10-30", "2026-10-31", "2026-11-01"}
	for i, d := range got {
		if d.Format("2006-01-02") != want[i] {
			t.Errorf("day %d = %s, want %s", i, d.Format("2006-01-02"), want[i])
		}
	}
	// The same instant in UTC is already 1 Nov (ECCC days are UTC days).
	if d := days(now, time.UTC, 0)[0]; d.Format("2006-01-02") != "2026-11-01" {
		t.Errorf("UTC day = %s, want 2026-11-01", d.Format("2006-01-02"))
	}
	// Offsets cross month and year ends.
	if d := days(time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), time.UTC, -1)[0]; d.Format("2006-01-02") != "2026-12-31" {
		t.Errorf("year boundary = %s, want 2026-12-31", d.Format("2006-01-02"))
	}
}

func TestJobsAreValid(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		key  string
		want int
	}{{"", 11}, {"some-key", 13}} {
		p := New(Config{AESOKey: tc.key, Log: quiet})
		jobs, err := p.Jobs()
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) != tc.want {
			t.Errorf("key %q: %d jobs, want %d", tc.key, len(jobs), tc.want)
		}
		// The scheduler rejects bad cron specs, duplicate names and missing timeouts.
		if _, err := scheduler.New(nil, quiet, jobs); err != nil {
			t.Errorf("key %q: %v", tc.key, err)
		}
	}
}

func TestTransformSkippedWhenNothingChanged(t *testing.T) {
	p := New(Config{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.dirty.Store(false)
	// A nil Transform would panic if the job tried to rebuild.
	if err := p.transformJob(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledJobsAreLeftOut(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	full, err := New(Config{Log: quiet}).Jobs()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := New(Config{Log: quiet, DisabledJobs: []string{"hydroquebec"}}).Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != len(full)-1 {
		t.Fatalf("got %d jobs, want %d", len(jobs), len(full)-1)
	}
	for _, j := range jobs {
		if j.Name == "hydroquebec" {
			t.Error("the hydroquebec job is still scheduled")
		}
	}
	if _, err := New(Config{Log: quiet, DisabledJobs: []string{"hydroquebc"}}).Jobs(); err == nil {
		t.Error("a misspelled job name must be an error")
	}
}
