package runtime

import (
	"context"
	"time"
)

type WaitFunc func(context.Context, time.Duration) error

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type boundedBackoff struct {
	current time.Duration
	initial time.Duration
	maximum time.Duration
}

func newBoundedBackoff(initial, maximum time.Duration) boundedBackoff {
	return boundedBackoff{current: initial, initial: initial, maximum: maximum}
}

func (backoff *boundedBackoff) Next() time.Duration {
	result := backoff.current
	backoff.current *= 2
	if backoff.current > backoff.maximum {
		backoff.current = backoff.maximum
	}
	return result
}

func (backoff *boundedBackoff) Reset() { backoff.current = backoff.initial }
