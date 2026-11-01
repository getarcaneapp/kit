package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	tests := []struct {
		name    string
		backoff Backoff
		attempt int
		want    time.Duration
	}{
		{name: "first attempt uses base", backoff: Backoff{Base: time.Second, Max: time.Minute}, want: time.Second},
		{name: "doubles per attempt", backoff: Backoff{Base: time.Second, Max: time.Minute}, attempt: 3, want: 8 * time.Second},
		{name: "caps at max", backoff: Backoff{Base: time.Second, Max: 5 * time.Second}, attempt: 3, want: 5 * time.Second},
		{name: "large attempt does not overflow", backoff: Backoff{Base: time.Second, Max: time.Minute}, attempt: 1000, want: time.Minute},
		{name: "zero max keeps base", backoff: Backoff{Base: 500 * time.Millisecond}, attempt: 4, want: 500 * time.Millisecond},
		{name: "zero value", backoff: Backoff{}, attempt: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.backoff.Delay(tt.attempt); got != tt.want {
				t.Fatalf("Delay(%d) = %s, want %s", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestBackoffDelayJitterStaysInUpperHalf(t *testing.T) {
	backoff := Backoff{
		Base:   4 * time.Second,
		Max:    time.Minute,
		Jitter: true,
	}
	for range 100 {
		if got := backoff.Delay(1); got < 4*time.Second || got >= 8*time.Second {
			t.Fatalf("Delay(1) = %s, want within [4s, 8s)", got)
		}
	}
}

func TestBackoffSleepEndsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (Backoff{Base: time.Hour}).Sleep(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep error = %v, want context canceled", err)
	}
	if err := (Backoff{Base: time.Millisecond}).Sleep(t.Context(), 0); err != nil {
		t.Fatalf("Sleep error = %v, want nil", err)
	}
}
