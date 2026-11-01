package tripwire

import (
	"cmp"
	"context"
	"log/slog"
	"time"

	kit "go.getarcane.app/kit/pkg"
)

const (
	defaultDebounce       = 2 * time.Second
	defaultMaxDelayFactor = 10
	defaultBackoffInitial = 5 * time.Second
	defaultBackoffMax     = 5 * time.Minute
)

// Config configures a [Watcher]. Only Key is required.
type Config[K comparable] struct {
	// Key maps an event to a batch key. Returning false drops the event.
	// Key runs on the watcher goroutine for every event, so keep it cheap.
	Key func(Event) (K, bool)
	// OnBatch receives debounced batches. Calls are serial: a slow OnBatch
	// delays the next batch, and keys keep merging while it runs. A panic is
	// recovered and logged. Nil discards batches.
	OnBatch func(context.Context, Batch[K])
	// SkipDir reports whether a directory with this base name should not be
	// watched or descended into. Nil watches everything.
	SkipDir func(name string) bool
	// Logger receives warnings about directories that could not be watched
	// and about lost subscriptions. Nil uses slog.Default().
	Logger *slog.Logger
	// Backoff controls how fast a lost subscription is retried.
	Backoff Backoff
	// Debounce is how long the tree must be quiet before a batch is delivered.
	// Zero means 2s.
	Debounce time.Duration
	// MaxDelay caps how long a batch can be held back by continuous changes.
	// Zero means 10*Debounce. Values below Debounce are raised to Debounce.
	MaxDelay time.Duration
	// MaxDepth limits how many directory levels below the root are watched.
	// Direct children of the root are depth 1. Zero or negative is unlimited.
	MaxDepth int
	// FollowSymlinks watches symlinked directories (and their targets).
	FollowSymlinks bool
}

// Backoff is an exponential retry delay.
type Backoff struct {
	// Initial is the delay after the first failed retry. Zero means 5s.
	Initial time.Duration
	// Max caps the delay. Zero means 5m.
	Max time.Duration
}

// resolve returns cfg with every default filled in.
func (c Config[K]) resolve() Config[K] {
	debounce := cmp.Or(c.Debounce, defaultDebounce)
	return Config[K]{
		Key:            c.Key,
		OnBatch:        kit.Ternary(c.OnBatch == nil, func(context.Context, Batch[K]) {}, c.OnBatch),
		SkipDir:        kit.Ternary(c.SkipDir == nil, func(string) bool { return false }, c.SkipDir),
		Logger:         cmp.Or(c.Logger, slog.Default()),
		Backoff:        c.Backoff.resolve(),
		Debounce:       debounce,
		MaxDelay:       max(cmp.Or(c.MaxDelay, defaultMaxDelayFactor*debounce), debounce),
		MaxDepth:       max(c.MaxDepth, 0),
		FollowSymlinks: c.FollowSymlinks,
	}
}

func (b Backoff) resolve() Backoff {
	initial := cmp.Or(b.Initial, defaultBackoffInitial)
	return Backoff{
		Initial: initial,
		Max:     max(cmp.Or(b.Max, defaultBackoffMax), initial),
	}
}
