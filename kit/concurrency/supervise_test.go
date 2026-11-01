package concurrency

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

var testSupervisorBackoff = Backoff{
	Base:    time.Millisecond,
	Max:     time.Second,
	Strikes: 3,
}

func waitForStatus(t *testing.T, supervisor *Supervisor, want SupervisorStatus) SupervisorState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if state := supervisor.State(); state.Status == want {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status = %q, want %q", supervisor.State().Status, want)
	return SupervisorState{}
}

func TestSuperviseRetriesFailuresUntilRunning(t *testing.T) {
	var calls atomic.Int32
	supervisor := Supervise(t.Context(), SuperviseConfig{
		Name:    "flaky",
		Backoff: testSupervisorBackoff,
		Run: func(ctx context.Context) error {
			if calls.Add(1) <= 5 {
				return errors.New("not ready")
			}
			<-ctx.Done()
			return ctx.Err()
		},
	})

	waitForStatus(t, supervisor, SupervisorRunning)
	for calls.Load() <= 5 {
		time.Sleep(time.Millisecond)
	}
	if err := supervisor.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := supervisor.State(); state.Status != SupervisorStopped {
		t.Fatalf("status after stop = %q, want %q", state.Status, SupervisorStopped)
	}
}

func TestSuperviseNeedsAttentionAfterStrikesUntilRestart(t *testing.T) {
	var calls atomic.Int32
	supervisor := Supervise(t.Context(), SuperviseConfig{
		Name:    "exits",
		Backoff: testSupervisorBackoff,
		Run: func(context.Context) error {
			if calls.Add(1)%2 == 0 {
				panic("deliberate supervisor panic")
			}
			return nil
		},
	})

	state := waitForStatus(t, supervisor, SupervisorNeedsAttention)
	if state.LastError == "" || state.NextRetry != nil {
		t.Fatalf("attention state = %+v, want an error and no retry", state)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("runs before attention = %d, want 3", got)
	}

	supervisor.Restart()
	for calls.Load() < 4 {
		time.Sleep(time.Millisecond)
	}
	if err := supervisor.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSuperviseRestartCancelsCurrentRun(t *testing.T) {
	var calls atomic.Int32
	supervisor := Supervise(t.Context(), SuperviseConfig{
		Name:    "restartable",
		Backoff: testSupervisorBackoff,
		Run: func(ctx context.Context) error {
			calls.Add(1)
			<-ctx.Done()
			return ctx.Err()
		},
	})

	waitForStatus(t, supervisor, SupervisorRunning)
	supervisor.Restart()
	for calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	if state := waitForStatus(t, supervisor, SupervisorRunning); state.LastError != "" {
		t.Fatalf("restart recorded an error: %q", state.LastError)
	}
	if err := supervisor.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}
