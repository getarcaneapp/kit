package concurrency

import (
	"context"
	"math/rand/v2"
	"time"
)

type (
	Backoff struct {
		Base    time.Duration
		Max     time.Duration
		Strikes int
		Jitter  bool
	}
)

func (b Backoff) Delay(attempt int) time.Duration {
	limit := max(b.Max, b.Base)
	delay := b.Base
	for range attempt {
		if delay >= limit/2 {
			delay = limit
			break
		}
		delay *= 2
	}
	if !b.Jitter || delay < 2 {
		return delay
	}
	return delay/2 + rand.N(delay/2)
}

func (b Backoff) Sleep(ctx context.Context, attempt int) error {
	timer := time.NewTimer(b.Delay(attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
