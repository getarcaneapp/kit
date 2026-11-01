// Package agg provides JSON-lines fan-in helpers for aggregated streams.
package agg

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	kit "go.getarcane.app/kit/pkg"
)

// RemoteEnvironmentLister lists the remote environment IDs an aggregated stream
// should cover.
type RemoteEnvironmentLister interface {
	ListRemoteEnvironmentIDs(ctx context.Context) ([]string, error)
}

// Producer sends events until ctx is canceled.
type Producer[T any] func(ctx context.Context, events chan<- T)

// Config configures Run.
type Config[T any] struct {
	Writer            io.Writer
	Flush             func()
	MakeHeartbeat     func() T
	Logger            *slog.Logger
	Producers         []Producer[T]
	Buffer            int
	HeartbeatInterval time.Duration
}

// InvalidConfigError describes an invalid Run configuration.
type InvalidConfigError struct {
	Field  string
	Reason string
}

func (e *InvalidConfigError) Error() string {
	if e == nil {
		return "invalid stream config"
	}
	return fmt.Sprintf("invalid stream config %s: %s", e.Field, e.Reason)
}

// Run drives a JSON-lines aggregated stream. It returns when ctx is canceled or
// encoding fails.
func Run[T any](ctx context.Context, cfg Config[T]) error {
	if cfg.Writer == nil {
		return &InvalidConfigError{Field: "Writer", Reason: "is required"}
	}
	if cfg.Flush == nil {
		return &InvalidConfigError{Field: "Flush", Reason: "is required"}
	}
	if cfg.Buffer < 0 {
		return &InvalidConfigError{Field: "Buffer", Reason: "must be non-negative"}
	}
	if cfg.MakeHeartbeat != nil && cfg.HeartbeatInterval <= 0 {
		return &InvalidConfigError{Field: "HeartbeatInterval", Reason: "must be positive when MakeHeartbeat is set"}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	events := make(chan T, cfg.Buffer)
	var wg sync.WaitGroup
	wg.Add(len(cfg.Producers))
	for _, producer := range cfg.Producers {
		go func(producer Producer[T]) {
			defer wg.Done()
			producer(streamCtx, events)
		}(producer)
	}
	defer wg.Wait()
	defer cancel()

	var heartbeat <-chan time.Time
	var ticker *time.Ticker
	if cfg.MakeHeartbeat != nil {
		ticker = time.NewTicker(cfg.HeartbeatInterval)
		defer ticker.Stop()
		heartbeat = ticker.C
	}

	for {
		select {
		case <-streamCtx.Done():
			return nil
		case event := <-events:
			if err := json.MarshalWrite(cfg.Writer, event); err != nil {
				return fmt.Errorf("encode stream event: %w", err)
			}
			if _, err := io.WriteString(cfg.Writer, "\n"); err != nil {
				return fmt.Errorf("terminate stream event: %w", err)
			}
			cfg.Flush()
		case <-heartbeat:
			if err := json.MarshalWrite(cfg.Writer, cfg.MakeHeartbeat()); err != nil {
				return fmt.Errorf("encode stream heartbeat: %w", err)
			}
			if _, err := io.WriteString(cfg.Writer, "\n"); err != nil {
				return fmt.Errorf("terminate stream heartbeat: %w", err)
			}
			cfg.Flush()
		}
	}
}

// Send forwards an event to the stream's event channel, giving up when ctx is
// done so producers can never block forever.
func Send[T any](ctx context.Context, events chan<- T, event T) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// PollerConfig configures ReconcilePollersByKey.
type PollerConfig[T any] struct {
	// List returns the items that should each have a running poller.
	List func(ctx context.Context) ([]T, error)
	// Key identifies an item. Items with an empty key are skipped.
	Key func(item T) string
	// Version restarts an item's poller when it changes. Nil never restarts.
	Version func(item T) string
	// Run is the poller body; it must return when ctx is canceled.
	Run func(ctx context.Context, item T)
	// Label names the stream in log messages.
	Label string
	// Interval is how often List is re-checked. Zero or negative means one minute.
	Interval time.Duration
}

// EnvironmentPollerConfig configures ReconcileEnvironmentPollers.
type EnvironmentPollerConfig struct {
	// Lister returns the remote environments that should each have a poller.
	Lister RemoteEnvironmentLister
	// Run is the poller body; it must return when ctx is canceled.
	Run func(ctx context.Context, environmentID string)
	// Label names the stream in log messages.
	Label string
	// Interval is how often Lister is re-checked. Zero or negative means one minute.
	Interval time.Duration
}

// ReconcileEnvironmentPollers keeps one poller goroutine per enabled remote
// environment until ctx is canceled.
func ReconcileEnvironmentPollers(ctx context.Context, cfg EnvironmentPollerConfig) {
	ReconcilePollersByKey(ctx, PollerConfig[string]{
		List:     cfg.Lister.ListRemoteEnvironmentIDs,
		Key:      func(environmentID string) string { return environmentID },
		Run:      cfg.Run,
		Label:    cfg.Label,
		Interval: cfg.Interval,
	})
}

// ReconcilePollersByKey keeps one poller goroutine per listed item, keyed by a
// stable item ID, until ctx is canceled.
func ReconcilePollersByKey[T any](ctx context.Context, cfg PollerConfig[T]) {
	set := pollerSet[T]{
		ctx:       ctx,
		active:    make(map[string]poller),
		runPoller: cfg.Run,
	}
	defer set.wg.Wait()
	defer func() {
		for _, activePoller := range set.active {
			activePoller.cancel()
		}
	}()

	reconcile := func() {
		items, err := cfg.List(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Default().WarnContext(ctx, "failed to list environments", "stream", cfg.Label, "error", err)
			}
			return
		}

		// Start, restart, and stop pollers so the set matches items exactly.
		current := make(map[string]struct{}, len(items))
		for _, item := range items {
			key := cfg.Key(item)
			if key == "" {
				continue
			}
			version := ""
			if cfg.Version != nil {
				version = cfg.Version(item)
			}
			current[key] = struct{}{}

			// Keep a poller running for key, restarting it when version changes.
			if existing, ok := set.active[key]; ok {
				if existing.version == version {
					continue
				}
				existing.cancel()
				select {
				case <-existing.done:
				case <-set.ctx.Done():
					<-existing.done
				}
				delete(set.active, key)
			}

			pollCtx, cancelPoll := context.WithCancel(set.ctx)
			done := make(chan struct{})
			set.active[key] = poller{
				cancel:  cancelPoll,
				done:    done,
				version: version,
			}
			set.wg.Go(func() {
				defer close(done)
				set.runPoller(pollCtx, item)
			})
		}

		for id, activePoller := range set.active {
			if _, ok := current[id]; !ok {
				activePoller.cancel()
				delete(set.active, id)
			}
		}
	}

	reconcile()

	ticker := time.NewTicker(kit.Ternary(cfg.Interval > 0, cfg.Interval, time.Minute))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

type poller struct {
	cancel  context.CancelFunc
	done    chan struct{}
	version string
}

// pollerSet tracks one running poller goroutine per key.
type pollerSet[T any] struct {
	ctx       context.Context
	active    map[string]poller
	runPoller func(ctx context.Context, item T)
	wg        sync.WaitGroup
}
