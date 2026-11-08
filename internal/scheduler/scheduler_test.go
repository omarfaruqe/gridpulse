package scheduler

import (
	"context"
	"testing"
	"time"
)

func ok(context.Context) error { return nil }

func TestNewValidatesJobs(t *testing.T) {
	cases := map[string][]Job{
		"no name":      {{Spec: "@every 1m", Timeout: time.Second, Run: ok}},
		"no run":       {{Name: "a", Spec: "@every 1m", Timeout: time.Second}},
		"no timeout":   {{Name: "a", Spec: "@every 1m", Run: ok}},
		"bad schedule": {{Name: "a", Spec: "every now and then", Timeout: time.Second, Run: ok}},
		"six fields":   {{Name: "a", Spec: "0 0 * * * *", Timeout: time.Second, Run: ok}},
		"duplicate":    {{Name: "a", Spec: "@every 1m", Timeout: time.Second, Run: ok}, {Name: "a", Spec: "@hourly", Timeout: time.Second, Run: ok}},
	}
	for name, jobs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(nil, nil, jobs); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestNewAcceptsValidSchedules(t *testing.T) {
	jobs := []Job{
		{Name: "a", Spec: "*/15 * * * *", Timeout: time.Second, Run: ok},
		{Name: "b", Spec: "20 * * * *", Timeout: time.Second, Run: ok},
		{Name: "c", Spec: "@every 10m", Timeout: time.Second, Run: ok},
	}
	s, err := New(nil, nil, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Names(); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("Names() = %v, want registration order a b c", got)
	}
}
