package agg

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type (
	failingWriter struct{}
)

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	err := Run(t.Context(), Config[string]{})
	if err == nil {
		t.Fatal("Run returned nil error for invalid config")
	}
}

func TestRunReturnsEncodeError(t *testing.T) {
	err := Run(t.Context(), Config[string]{
		Writer: failingWriter{},
		Flush:  func() {},
		Producers: []Producer[string]{
			func(ctx context.Context, events chan<- string) {
				Send(ctx, events, "event")
			},
		},
	})
	if err == nil {
		t.Fatal("Run returned nil error after encoder failure")
	}
}

func TestRunAllowsNoHeartbeat(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var out bytes.Buffer
	err := Run(ctx, Config[string]{
		Writer: &out,
		Flush:  cancel,
		Producers: []Producer[string]{
			func(ctx context.Context, events chan<- string) {
				Send(ctx, events, "event")
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := out.String(); got != "\"event\"\n" {
		t.Fatalf("encoded output = %q, want event JSON line", got)
	}
}

func TestRunEncodesInvalidUTF8(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var out bytes.Buffer
	err := Run(ctx, Config[string]{
		Writer: &out,
		Flush:  cancel,
		Producers: []Producer[string]{
			func(ctx context.Context, events chan<- string) {
				Send(ctx, events, "bad \xff")
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := out.String(); got != "\"bad \ufffd\"\n" {
		t.Fatalf("encoded output = %q, want replacement character", got)
	}
}

func TestReconcilePollersWaitsForReplacementToExit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan string, 2)
	releaseFirst := make(chan struct{})
	calls := 0
	items := [][]string{{"v1"}, {"v2"}}

	go ReconcilePollersByKey(ctx, PollerConfig[string]{
		List: func(context.Context) ([]string, error) {
			if calls >= len(items) {
				cancel()
				return nil, nil
			}
			out := items[calls]
			calls++
			return out, nil
		},
		Key:     func(string) string { return "same" },
		Version: func(version string) string { return version },
		Run: func(ctx context.Context, item string) {
			started <- item
			if item == "v1" {
				<-releaseFirst
			}
		},
		Label:    "test stream",
		Interval: 10 * time.Millisecond,
	})

	first := <-started
	if first != "v1" {
		t.Fatalf("first poller = %q, want v1", first)
	}

	select {
	case replacement := <-started:
		t.Fatalf("replacement started before first exited: %q", replacement)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseFirst)

	select {
	case replacement := <-started:
		if replacement != "v2" {
			t.Fatalf("replacement = %q, want v2", replacement)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not start after first exited")
	}
}

func TestReconcilePollersWaitsForRemovedPollerToExit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{}, 2)
	releaseFirst := make(chan struct{})
	var runs atomic.Int32
	calls := 0
	items := [][]string{{"a"}, nil, {"a"}}

	go ReconcilePollersByKey(ctx, PollerConfig[string]{
		List: func(context.Context) ([]string, error) {
			if calls >= len(items) {
				cancel()
				return nil, nil
			}
			out := items[calls]
			calls++
			return out, nil
		},
		Key: func(item string) string { return item },
		Run: func(context.Context, string) {
			started <- struct{}{}
			if runs.Add(1) == 1 {
				<-releaseFirst
			}
		},
		Label:    "test stream",
		Interval: 10 * time.Millisecond,
	})

	<-started
	select {
	case <-started:
		t.Fatal("second poller started while the removed one was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
}
