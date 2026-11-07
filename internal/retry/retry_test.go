package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDoSucceedsAfterFailures(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("boom")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err = %v, calls = %d, want nil and 3", err, calls)
	}
}

func TestDoGivesUp(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 2, time.Millisecond, func() error {
		calls++
		return errors.New("boom")
	})
	if err == nil || calls != 2 {
		t.Fatalf("err = %v, calls = %d, want error and 2", err, calls)
	}
}

func TestDoStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Do(ctx, 5, time.Hour, func() error { return errors.New("boom") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
