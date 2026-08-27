package agg

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestHubSharesRunnerAcrossSubscribersAndReplaysLast(t *testing.T) {
	hub := NewHub[int]()

	var starts atomic.Int32
	runner := func(ctx context.Context, publish func(int)) {
		starts.Add(1)
		publish(42)
		<-ctx.Done()
	}

	receive := func(events <-chan int) int {
		t.Helper()
		select {
		case v := <-events:
			return v
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for event")
			return 0
		}
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	events1 := make(chan int, 4)
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		hub.Subscribe(ctx1, "env-1", runner, func(v int) bool {
			events1 <- v
			return true
		})
	}()
	if got := receive(events1); got != 42 {
		t.Fatalf("first subscriber received %d, want 42", got)
	}

	// A second subscriber reuses the running poller and immediately gets the
	// last published event.
	ctx2, cancel2 := context.WithCancel(context.Background())
	events2 := make(chan int, 4)
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		hub.Subscribe(ctx2, "env-1", runner, func(v int) bool {
			events2 <- v
			return true
		})
	}()
	if got := receive(events2); got != 42 {
		t.Fatalf("second subscriber received %d, want 42", got)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("runner started %d times with concurrent subscribers, want 1", got)
	}

	cancel1()
	cancel2()
	<-done1
	<-done2

	// With every subscriber gone the runner was stopped; the next subscriber
	// starts a fresh one.
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	events3 := make(chan int, 4)
	go hub.Subscribe(ctx3, "env-1", runner, func(v int) bool {
		events3 <- v
		return true
	})
	if got := receive(events3); got != 42 {
		t.Fatalf("third subscriber received %d, want 42", got)
	}
	if got := starts.Load(); got != 2 {
		t.Fatalf("runner started %d times after resubscribe, want 2", got)
	}
}
