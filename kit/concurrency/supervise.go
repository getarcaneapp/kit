package concurrency

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type (
	SupervisorStatus string

	SupervisorState struct {
		UpdatedAt time.Time
		NextRetry *time.Time
		Status    SupervisorStatus
		LastError string
	}

	SuperviseConfig struct {
		Run     func(context.Context) error
		Logger  *slog.Logger
		Name    string
		Backoff Backoff
	}

	Supervisor struct {
		cancel    context.CancelFunc
		cancelRun context.CancelFunc
		restart   chan struct{}
		done      chan struct{}
		state     SupervisorState
		mu        sync.Mutex
	}
)

const (
	SupervisorStarting       SupervisorStatus = "starting"
	SupervisorRunning        SupervisorStatus = "running"
	SupervisorRetrying       SupervisorStatus = "retrying"
	SupervisorNeedsAttention SupervisorStatus = "needs_attention"
	SupervisorStopped        SupervisorStatus = "stopped"
)

var (
	defaultSupervisorBackoff = Backoff{
		Base:    5 * time.Second,
		Max:     5 * time.Minute,
		Strikes: 3,
		Jitter:  true,
	}
	errExitedUnexpectedly = errors.New("background operation exited unexpectedly")
)

func Supervise(ctx context.Context, cfg SuperviseConfig) *Supervisor {
	loopCtx, cancel := context.WithCancel(ctx)
	s := &Supervisor{
		cancel:  cancel,
		restart: make(chan struct{}, 1),
		done:    make(chan struct{}),
		state: SupervisorState{
			UpdatedAt: time.Now(),
			Status:    SupervisorStarting,
		},
	}
	go s.loop(loopCtx, SuperviseConfig{
		Run:    cfg.Run,
		Logger: cmp.Or(cfg.Logger, slog.Default()),
		Name:   cfg.Name,
		Backoff: Backoff{
			Base:    cmp.Or(cfg.Backoff.Base, defaultSupervisorBackoff.Base),
			Max:     cmp.Or(cfg.Backoff.Max, defaultSupervisorBackoff.Max),
			Strikes: cmp.Or(cfg.Backoff.Strikes, defaultSupervisorBackoff.Strikes),
			Jitter:  cmp.Or(cfg.Backoff, defaultSupervisorBackoff).Jitter,
		},
	})
	return s
}

func (s *Supervisor) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case s.restart <- struct{}{}:
	default:
	}
	if s.cancelRun != nil {
		s.cancelRun()
	}
}

func (s *Supervisor) Stop(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Supervisor) State() SupervisorState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Supervisor) loop(ctx context.Context, cfg SuperviseConfig) {
	defer close(s.done)
	defer s.setState(SupervisorStopped, "", nil)
	attempt, strikes := 0, 0
	for ctx.Err() == nil {
		started := time.Now()
		panicked, err := s.runOnce(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-s.restart:
			attempt, strikes = 0, 0
			continue
		default:
		}
		if time.Since(started) > cfg.Backoff.Max {
			attempt, strikes = 0, 0
		}
		if panicked || err == nil {
			strikes++
		}
		err = cmp.Or(err, errExitedUnexpectedly)
		if cfg.Backoff.Strikes > 0 && strikes >= cfg.Backoff.Strikes {
			cfg.Logger.ErrorContext(ctx, "Background operation requires attention", "name", cfg.Name, "error", err)
			s.setState(SupervisorNeedsAttention, err.Error(), nil)
			select {
			case <-ctx.Done():
			case <-s.restart:
				attempt, strikes = 0, 0
			}
			continue
		}
		next := time.Now().Add(cfg.Backoff.Delay(attempt))
		cfg.Logger.WarnContext(ctx, "Background operation failed; retrying", "name", cfg.Name, "error", err, "retryIn", time.Until(next))
		s.setState(SupervisorRetrying, err.Error(), &next)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
		case <-s.restart:
			attempt, strikes = 0, 0
		case <-timer.C:
			attempt++
		}
		timer.Stop()
	}
}

func (s *Supervisor) runOnce(ctx context.Context, cfg SuperviseConfig) (panicked bool, err error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.cancelRun = cancel
	s.state = SupervisorState{
		UpdatedAt: time.Now(),
		Status:    SupervisorRunning,
	}
	s.mu.Unlock()
	panicked = true
	defer RecoverToError(&err, cfg.Name)
	return false, cfg.Run(runCtx)
}

func (s *Supervisor) setState(status SupervisorStatus, lastError string, next *time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = SupervisorState{
		UpdatedAt: time.Now(),
		NextRetry: next,
		Status:    status,
		LastError: lastError,
	}
}
