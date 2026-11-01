package tripwire

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// eventBuffer sizes the fsnotify event channel so bursts don't back up into
// the kernel queue while the loop is busy attaching a new directory.
const eventBuffer = 1024

var (
	// ErrNoKey is returned by [New] when [Config.Key] is nil.
	ErrNoKey = errors.New("tripwire: Config.Key is required")
	// ErrNoRoot is returned by [New] when the root path is empty.
	ErrNoRoot = errors.New("tripwire: root path is required")
	// ErrStopped is returned by [Watcher.Start] after Stop or a failed Start.
	ErrStopped = errors.New("tripwire: watcher stopped")
)

// Watcher watches a directory tree and delivers debounced batches of keys.
// Create one with [New]. A Watcher runs once: after Stop it cannot restart.
type Watcher[K comparable] struct {
	closeErr error
	ready    map[K]struct{}
	wake     chan struct{}
	done     chan struct{}
	cancel   context.CancelFunc
	root     string
	cfg      Config[K]
	delivery sync.WaitGroup
	stopOnce sync.Once
	lifeMu   sync.Mutex
	readyMu  sync.Mutex
	resync   bool
	started  bool
	stopped  bool
}

// New returns a watcher for root. It does not touch the filesystem until
// [Watcher.Start].
func New[K comparable](root string, cfg Config[K]) (*Watcher[K], error) {
	switch {
	case root == "":
		return nil, ErrNoRoot
	case cfg.Key == nil:
		return nil, ErrNoKey
	}
	return &Watcher[K]{
		cfg:   cfg.resolve(),
		ready: make(map[K]struct{}),
		wake:  make(chan struct{}, 1),
		done:  make(chan struct{}),
		root:  filepath.Clean(root),
	}, nil
}

// Start subscribes to the tree and begins delivering batches. When Start
// returns nil, every existing directory within the depth limit is watched.
// The watcher runs until Stop is called or ctx is done. Start is a no-op on a
// running watcher and returns [ErrStopped] after Stop. If Start fails, the watcher is spent and Start returns the
// error; calling Stop afterwards is safe but not required.
func (w *Watcher[K]) Start(ctx context.Context) error {
	w.lifeMu.Lock()
	defer w.lifeMu.Unlock()

	switch {
	case w.stopped:
		return ErrStopped
	case w.started:
		return nil
	}

	t := &tree{
		aliases:  make(map[string]string),
		skip:     w.cfg.SkipDir,
		log:      w.cfg.Logger,
		root:     w.root,
		prefix:   strings.TrimSuffix(w.root, sep) + sep,
		maxDepth: w.cfg.MaxDepth,
		follow:   w.cfg.FollowSymlinks,
	}
	fsw, err := w.open(ctx, t)
	if err != nil {
		w.stopped = true
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.started = true
	go w.run(runCtx, fsw, t)
	w.delivery.Go(func() { w.deliver(runCtx) })
	return nil
}

// Stop shuts the watcher down and waits for an in-flight OnBatch to return.
// Changes not yet delivered are dropped. Stop is idempotent, safe to call
// concurrently, and safe to call before Start.
func (w *Watcher[K]) Stop() error {
	w.stopOnce.Do(func() {
		w.lifeMu.Lock()
		w.stopped = true
		cancel := w.cancel
		w.lifeMu.Unlock()

		if cancel != nil {
			cancel()
			<-w.done
		}
	})
	w.delivery.Wait()
	return w.closeErr
}

// open creates a subscription with the root and every directory below it.
func (w *Watcher[K]) open(ctx context.Context, t *tree) (*fsnotify.Watcher, error) {
	fsw, err := fsnotify.NewBufferedWatcher(eventBuffer)
	if err != nil {
		return nil, err
	}
	if addErr := fsw.Add(w.root); addErr != nil {
		return nil, errors.Join(addErr, fsw.Close())
	}
	clear(t.aliases)
	if walkErr := t.walk(ctx, fsw, frame{dir: w.root, logical: w.root}, nil); walkErr != nil {
		w.cfg.Logger.WarnContext(ctx, "tripwire: failed to watch some directories", "path", w.root, "error", walkErr)
	}
	return fsw, nil
}

// pending is the batch being debounced. It lives on the run goroutine.
type pending[K comparable] struct {
	keys   map[K]struct{}
	first  time.Time
	resync bool
}

// run owns the subscription, the tree, and the debounce timer. Nothing else
// touches them, so reconnecting never races with Stop.
func (w *Watcher[K]) run(ctx context.Context, fsw *fsnotify.Watcher, t *tree) {
	defer close(w.done)
	defer func() { w.closeErr = fsw.Close() }()

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	p := &pending[K]{keys: make(map[K]struct{})}
	for {
		var lost bool
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fsw.Events:
			// A closed channel or the root itself going away both need a
			// fresh subscription.
			lost = !ok || (ev.Name == w.root && ev.Has(fsnotify.Remove|fsnotify.Rename))
			if !lost && w.handle(ctx, fsw, t, ev, p) {
				w.schedule(timer, p)
			}
		case err, ok := <-fsw.Errors:
			lost = !ok
			if !lost && w.handleErr(ctx, err, p) {
				w.schedule(timer, p)
			}
		case <-timer.C:
			w.flush(p)
		}
		if !lost {
			continue
		}
		next, ok := w.reconnect(ctx, fsw, t)
		fsw = next
		if !ok {
			return
		}
		p.resync = true
		w.schedule(timer, p)
	}
}

// handle updates subscriptions for one event and offers it to Key. It
// reports whether anything was added to the pending batch.
func (w *Watcher[K]) handle(ctx context.Context, fsw *fsnotify.Watcher, t *tree, ev fsnotify.Event, p *pending[K]) bool {
	logical := t.logical(ev.Name)
	if ev.Has(fsnotify.Remove | fsnotify.Rename) {
		t.forget(fsw, logical)
	}

	added := false
	if ev.Has(fsnotify.Create) && !w.cfg.SkipDir(filepath.Base(ev.Name)) {
		t.attach(ctx, fsw, ev.Name, logical, func(realPath, entryLogical string) {
			added = w.offer(t, realPath, entryLogical, fsnotify.Create, p) || added
		})
	}
	return w.offer(t, ev.Name, logical, ev.Op, p) || added
}

// offer maps one change to a key and adds it to the pending batch.
func (w *Watcher[K]) offer(t *tree, realPath, logical string, op Op, p *pending[K]) bool {
	rel := t.rel(logical)
	if depth(rel) < 0 {
		return false
	}
	key, ok := w.cfg.Key(Event{
		Path:    realPath,
		Logical: logical,
		Rel:     rel,
		Op:      op,
	})
	if ok {
		p.keys[key] = struct{}{}
	}
	return ok
}

// handleErr reports whether the error needs a resync batch.
func (w *Watcher[K]) handleErr(ctx context.Context, err error, p *pending[K]) bool {
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		w.cfg.Logger.WarnContext(ctx, "tripwire: event queue overflowed; requesting resync", "path", w.root)
		p.resync = true
		return true
	}
	w.cfg.Logger.WarnContext(ctx, "tripwire: watch error", "path", w.root, "error", err)
	return false
}

// schedule arms the timer for min(now+Debounce, first+MaxDelay).
func (w *Watcher[K]) schedule(timer *time.Timer, p *pending[K]) {
	now := time.Now()
	if p.first.IsZero() {
		p.first = now
	}
	timer.Reset(min(w.cfg.Debounce, p.first.Add(w.cfg.MaxDelay).Sub(now)))
}

// flush hands the pending batch to the delivery goroutine.
func (w *Watcher[K]) flush(p *pending[K]) {
	if len(p.keys) == 0 && !p.resync {
		return
	}
	w.readyMu.Lock()
	maps.Copy(w.ready, p.keys)
	w.resync = w.resync || p.resync
	w.readyMu.Unlock()

	clear(p.keys)
	*p = pending[K]{keys: p.keys}

	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// deliver calls OnBatch serially. Keys flushed while a call runs merge into
// the next batch.
func (w *Watcher[K]) deliver(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}
		if ctx.Err() != nil {
			return
		}
		w.readyMu.Lock()
		batch := Batch[K]{
			Keys:   slices.Collect(maps.Keys(w.ready)),
			Resync: w.resync,
		}
		clear(w.ready)
		w.resync = false
		w.readyMu.Unlock()

		if len(batch.Keys) > 0 || batch.Resync {
			w.call(ctx, batch)
		}
	}
}

func (w *Watcher[K]) call(ctx context.Context, batch Batch[K]) {
	defer func() {
		if r := recover(); r != nil {
			w.cfg.Logger.ErrorContext(ctx, "tripwire: OnBatch panicked",
				"path", w.root,
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()
	w.cfg.OnBatch(ctx, batch)
}

// reconnect replaces a lost subscription, retrying with backoff until it
// succeeds or ctx is done. On failure it returns the closed old watcher so
// the caller always holds a non-nil value.
func (w *Watcher[K]) reconnect(ctx context.Context, old *fsnotify.Watcher, t *tree) (*fsnotify.Watcher, bool) {
	if err := old.Close(); err != nil {
		w.cfg.Logger.DebugContext(ctx, "tripwire: closing lost subscription", "error", err)
	}
	delay := w.cfg.Backoff.Initial
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if !sleep(ctx, delay) {
				return old, false
			}
			delay = min(delay*2, w.cfg.Backoff.Max)
		}
		fsw, err := w.open(ctx, t)
		if err == nil {
			w.cfg.Logger.InfoContext(ctx, "tripwire: subscription restored", "path", w.root)
			return fsw, true
		}
		if attempt == 0 {
			w.cfg.Logger.WarnContext(ctx, "tripwire: subscription lost; retrying", "path", w.root, "error", err)
		}
		if ctx.Err() != nil {
			return old, false
		}
	}
}

// sleep waits for d and reports false if ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
