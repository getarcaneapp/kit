package tripwire

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"kit.getarcane.app"
)

type (
	// Config configures a [Watcher]. Roots and Key are required.
	Config[K comparable] struct {
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
		// and about lost roots and subscriptions. Nil uses slog.Default().
		Logger *slog.Logger
		// Roots are the directory trees to watch. Each is cleaned with
		// filepath.Clean; duplicates (equal once made absolute) are dropped,
		// keeping the first spelling. Roots must not be nested inside one
		// another. Order matters only for symlinks: when links under two roots
		// point at the same directory, events there are reported under the
		// earlier root.
		Roots []string
		// Backoff controls how fast a lost root or subscription is retried.
		Backoff Backoff
		// Debounce is how long every root must be quiet before a batch is
		// delivered. Zero means 2s.
		Debounce time.Duration
		// MaxDelay caps how long a batch can be held back by continuous changes.
		// Zero means 10*Debounce. Values below Debounce are raised to Debounce.
		MaxDelay time.Duration
		// MaxDepth limits how many directory levels below each root are watched.
		// Direct children of a root are depth 1. Zero or negative is unlimited.
		MaxDepth int
		// FollowSymlinks watches symlinked directories (and their targets). A
		// link whose target is a root, lies inside one, or contains one is not
		// followed, since that root already reports those directories under
		// its own path.
		FollowSymlinks bool
	}

	// Backoff is an exponential retry delay.
	Backoff struct {
		// Initial is the delay before the first retry. Zero means 5s.
		Initial time.Duration
		// Max caps the delay. Zero means 5m.
		Max time.Duration
	}
)

const (
	defaultDebounce       = 2 * time.Second
	defaultMaxDelayFactor = 10
	defaultBackoffInitial = 5 * time.Second
	defaultBackoffMax     = 5 * time.Minute
)

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

// roots returns the cleaned, deduplicated roots, or an error wrapping
// [ErrNoRoot] or [ErrNestedRoots].
func (c Config[K]) roots() ([]string, error) {
	if len(c.Roots) == 0 {
		return nil, ErrNoRoot
	}
	roots := make([]string, 0, len(c.Roots))
	abs := make([]string, 0, len(c.Roots))
	for _, path := range c.Roots {
		if path == "" {
			return nil, ErrNoRoot
		}
		root := filepath.Clean(path)
		full, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("tripwire: resolve root %s: %w", root, err)
		}
		if slices.Contains(abs, full) {
			continue
		}
		for i, other := range abs {
			if strings.HasPrefix(full, withSep(other)) || strings.HasPrefix(other, withSep(full)) {
				return nil, fmt.Errorf("%w: %s and %s", ErrNestedRoots, roots[i], root)
			}
		}
		roots = append(roots, root)
		abs = append(abs, full)
	}
	return roots, nil
}

func (b Backoff) resolve() Backoff {
	initial := cmp.Or(b.Initial, defaultBackoffInitial)
	return Backoff{
		Initial: initial,
		Max:     max(cmp.Or(b.Max, defaultBackoffMax), initial),
	}
}
