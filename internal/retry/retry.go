// Package retry runs a function with exponential backoff and jitter.
package retry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

// Do calls fn up to attempts times, doubling the delay (plus jitter) between
// tries. It stops early if ctx is cancelled.
func Do(ctx context.Context, attempts int, base time.Duration, fn func() error) error {
	var err error
	delay := base
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		wait := delay + rand.N(delay/2+1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
	}
	return fmt.Errorf("after %d attempts: %w", attempts, err)
}
