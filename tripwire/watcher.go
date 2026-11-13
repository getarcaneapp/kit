package tripwire

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type (
	// Watcher watches one or more directory trees through a single
	// subscription and delivers debounced batches of keys. Changes under any
	// root share one debounce window, so a burst spanning several roots
	// arrives as one [Batch]. Create one with [New]. A Watcher runs once:
	// after Stop it cannot restart.
	//
	// Roots are lost and restored independently. When a root is removed or
	// renamed its watches are dropped, a Resync batch is requested straight
	// away (its contents vanished without per-file events), and the other
	// roots keep delivering. Lost roots are retried together on one
	// [Config.Backoff] schedule, which restarts at Backoff.Initial whenever
	// another root is lost. A root that comes back is walked again and a
	// second Resync batch is requested.
	Watcher[K comparable] struct {
		closeErr error
		ready    map[K]struct{}
		wake     chan struct{}
		done     chan struct{}
		cancel   context.CancelFunc
		roots    []string
		cfg      Config[K]
		delivery sync.WaitGroup
		stopOnce sync.Once
		lifeMu   sync.Mutex
		readyMu  sync.Mutex
		resync   bool
		started  bool
		stopped  bool
	}

	// pending is the batch being debounced. It lives on the run goroutine.
	pending[K comparable] struct {
		keys   map[K]struct{}
		first  time.Time
		resync bool
	}
)

const (
	// eventBuffer sizes the fsnotify event channel so bursts don't back up into
	// the kernel queue while the loop is busy attaching a new directory.
	eventBuffer = 1024
)

var (
	// ErrNoKey is returned by [New] when [Config.Key] is nil.
	ErrNoKey = errors.New("tripwire: Config.Key is required")
	// ErrNoRoot is returned by [New] when [Config.Roots] is empty or holds an
	// empty path.
	ErrNoRoot = errors.New("tripwire: Config.Roots needs at least one non-empty path")
	// ErrNestedRoots is returned by [New], wrapped with both paths, when one
	// root lies inside another. The check is lexical; symlinks are not
	// resolved.
	ErrNestedRoots = errors.New("tripwire: roots must not be nested")
	// ErrStopped is returned by [Watcher.Start] after Stop or a failed Start.
	ErrStopped = errors.New("tripwire: watcher stopped")
)

// New returns a watcher for cfg.Roots. It validates the config but does not
// touch the filesystem until [Watcher.Start].
func New[K comparable](cfg Config[K]) (*Watcher[K], error) {
	roots, err := cfg.roots()
	switch {
	case err != nil:
		return nil, err
	case cfg.Key == nil:
		return nil, ErrNoKey
	}
	return &Watcher[K]{
		cfg:   cfg.resolve(),
		ready: make(map[K]struct{}),
		wake:  make(chan struct{}, 1),
		done:  make(chan struct{}),
		roots: roots,
	}, nil
}

// Start subscribes to every root and begins delivering batches. When Start
// returns nil, every root and every existing directory within the depth limit
// under each root is watched. If any root cannot be subscribed, Start returns
// the joined errors naming each failed root, releases the subscription, and
// the watcher is spent; calling Stop afterwards is safe but not required. The
// watcher runs until Stop is called or ctx is done. Start is a no-op on a
// running watcher and returns [ErrStopped] after Stop.
func (w *Watcher[K]) Start(ctx context.Context) error {
	w.lifeMu.Lock()
	defer w.lifeMu.Unlock()

	switch {
	case w.stopped:
		return ErrStopped
	case w.started:
		return nil
	}

	t := newTree(w.roots, w.cfg)
	fsw, err := fsnotify.NewBufferedWatcher(eventBuffer)
	if err != nil {
		w.stopped = true
		return err
	}
	if addErr := w.addRoots(ctx, fsw, t); addErr != nil {
		w.stopped = true
		return errors.Join(addErr, fsw.Close())
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

// addRoots subscribes every root on a fresh fsw. Roots that fail are marked
// lost and their errors are joined into the result. Every root is subscribed
// before any is walked, so links between roots are recognised whatever the
// order.
func (w *Watcher[K]) addRoots(ctx context.Context, fsw *fsnotify.Watcher, t *tree) error {
	clear(t.aliases)
	clear(t.lost)
	t.watched = nil
	var errs []error
	for i, root := range t.roots {
		if err := t.subscribe(fsw, i); err != nil {
			t.lost[root.path] = struct{}{}
			errs = append(errs, fmt.Errorf("tripwire: watch root %s: %w", root.path, err))
		}
	}
	for i, root := range t.roots {
		if _, gone := t.lost[root.path]; gone {
			continue
		}
		if err := t.walkRoot(ctx, fsw, i); err != nil {
			t.lost[root.path] = struct{}{}
			errs = append(errs, fmt.Errorf("tripwire: watch root %s: %w", root.path, err))
		}
	}
	return errors.Join(errs...)
}

// run owns the subscription, the tree, and the debounce and retry timers.
// Nothing else touches them, so reconnecting never races with Stop.
func (w *Watcher[K]) run(ctx context.Context, fsw *fsnotify.Watcher, t *tree) {
	defer close(w.done)
	defer func() { w.closeErr = fsw.Close() }()

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	// retry fires while roots are lost; delay is its current backoff.
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	var delay time.Duration

	p := &pending[K]{keys: make(map[K]struct{})}
	for {
		lostBefore := len(t.lost)
		closed := false
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fsw.Events:
			closed = !ok
			switch {
			case closed:
			case w.lose(ctx, fsw, t, ev):
				p.resync = true
				w.schedule(timer, p)
			case w.handle(ctx, fsw, t, ev, p):
				w.schedule(timer, p)
			}
		case err, ok := <-fsw.Errors:
			closed = !ok
			if !closed && w.handleErr(ctx, err, p) {
				w.schedule(timer, p)
			}
		case <-timer.C:
			w.flush(p)
		case <-retry.C:
			if w.restore(ctx, fsw, t) {
				p.resync = true
				w.schedule(timer, p)
			}
			if len(t.lost) > 0 {
				delay = min(delay*2, w.cfg.Backoff.Max)
				retry.Reset(delay)
			}
		}
		if closed {
			next, ok := w.reopen(ctx, fsw, t)
			fsw = next
			if !ok {
				return
			}
			p.resync = true
			w.schedule(timer, p)
		}
		if len(t.lost) > lostBefore {
			delay = w.cfg.Backoff.Initial
			retry.Reset(delay)
		}
	}
}

// lose reports whether ev removed or renamed a watched root. If so, the root
// is detached and re-added once straight away in case it was replaced in the
// same step; if that fails it is marked lost for the retry timer.
func (w *Watcher[K]) lose(ctx context.Context, fsw *fsnotify.Watcher, t *tree, ev fsnotify.Event) bool {
	if !ev.Has(fsnotify.Remove | fsnotify.Rename) {
		return false
	}
	i, rel := t.locate(ev.Name)
	if i < 0 || rel != "." {
		return false
	}
	root := t.roots[i]
	if _, gone := t.lost[root.path]; gone {
		return true
	}
	t.detach(fsw, root)
	if err := t.add(ctx, fsw, i); err == nil {
		w.cfg.Logger.InfoContext(ctx, "tripwire: root replaced; resubscribed", "path", root.path)
		return true
	}
	t.lost[root.path] = struct{}{}
	w.cfg.Logger.WarnContext(ctx, "tripwire: root lost; retrying", "path", root.path, "op", ev.Op.String())
	return true
}

// restore retries every lost root once and reports whether any came back.
func (w *Watcher[K]) restore(ctx context.Context, fsw *fsnotify.Watcher, t *tree) bool {
	restored := false
	for i, root := range t.roots {
		if _, gone := t.lost[root.path]; !gone {
			continue
		}
		if err := t.add(ctx, fsw, i); err != nil {
			w.cfg.Logger.DebugContext(ctx, "tripwire: root still unavailable", "path", root.path, "error", err)
			continue
		}
		delete(t.lost, root.path)
		w.cfg.Logger.InfoContext(ctx, "tripwire: root restored", "path", root.path)
		restored = true
	}
	return restored
}

// handle updates subscriptions for one event and offers it to Key. It
// reports whether anything was added to the pending batch.
func (w *Watcher[K]) handle(ctx context.Context, fsw *fsnotify.Watcher, t *tree, ev fsnotify.Event, p *pending[K]) bool {
	logical := t.logical(ev.Name)
	if ev.Has(fsnotify.Remove | fsnotify.Rename) {
		t.forget(fsw, logical)
		t.unwatch(fsw, ev.Name)
	}

	added := false
	if ev.Has(fsnotify.Create) && !w.cfg.SkipDir(filepath.Base(ev.Name)) {
		t.attach(ctx, fsw, ev.Name, logical, func(realPath, entryLogical string) {
			added = w.offer(t, realPath, entryLogical, fsnotify.Create, p) || added
		})
	}
	return w.offer(t, ev.Name, logical, ev.Op, p) || added
}

// offer maps one change to a key and adds it to the pending batch. Changes
// outside every root, under a lost root, or deeper than the entries of a
// MaxDepth directory are dropped; the last can happen below a symlink target
// that another root reaches at a shallower depth.
func (w *Watcher[K]) offer(t *tree, realPath, logical string, op Op, p *pending[K]) bool {
	i, rel := t.locate(logical)
	if i < 0 || (t.maxDepth > 0 && depth(rel) > t.maxDepth+1) {
		return false
	}
	if _, gone := t.lost[t.roots[i].path]; gone {
		return false
	}
	key, ok := w.cfg.Key(Event{
		Path:    realPath,
		Logical: logical,
		Root:    t.roots[i].path,
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
		w.cfg.Logger.WarnContext(ctx, "tripwire: event queue overflowed; requesting resync", "roots", w.roots)
		p.resync = true
		return true
	}
	w.cfg.Logger.WarnContext(ctx, "tripwire: watch error", "roots", w.roots, "error", err)
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
				"roots", w.roots,
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()
	w.cfg.OnBatch(ctx, batch)
}

// reopen replaces a subscription whose channels closed and re-adds every
// root. Creating the subscription is retried with backoff until it succeeds
// or ctx is done; with no subscription there are no events to fall behind
// on. Roots that cannot be re-added, even all of them, are left lost for the
// run loop's retry timer. On failure it returns the closed old watcher so
// the caller always holds a non-nil value.
func (w *Watcher[K]) reopen(ctx context.Context, old *fsnotify.Watcher, t *tree) (*fsnotify.Watcher, bool) {
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
		fsw, err := fsnotify.NewBufferedWatcher(eventBuffer)
		if err == nil {
			if addErr := w.addRoots(ctx, fsw, t); addErr != nil {
				w.cfg.Logger.WarnContext(ctx, "tripwire: some roots unavailable after resubscribing; retrying", "error", addErr)
			}
			w.cfg.Logger.InfoContext(ctx, "tripwire: subscription restored", "roots", w.roots)
			return fsw, true
		}
		if attempt == 0 {
			w.cfg.Logger.WarnContext(ctx, "tripwire: subscription lost; retrying", "roots", w.roots, "error", err)
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
