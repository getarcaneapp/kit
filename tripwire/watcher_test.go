package tripwire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

type (
	// recorder collects batches on a channel.
	recorder[K comparable] struct {
		batches chan Batch[K]
	}
)

const (
	fastDebounce = 25 * time.Millisecond
	waitFor      = 3 * time.Second
	quietFor     = 500 * time.Millisecond
)

var fastBackoff = Backoff{
	Initial: 20 * time.Millisecond,
	Max:     50 * time.Millisecond,
}

// composeKey stands in for arcane's project-file filter.
func composeKey(e Event) (string, bool) {
	switch filepath.Base(e.Path) {
	case "compose.yaml", "docker-compose.yml":
		return e.Logical, true
	default:
		return "", false
	}
}

func (r *recorder[K]) onBatch(_ context.Context, b Batch[K]) {
	r.batches <- b
}

func (r *recorder[K]) next(t *testing.T, why string) Batch[K] {
	t.Helper()
	select {
	case b := <-r.batches:
		return b
	case <-time.After(waitFor):
		t.Fatalf("timed out: %s", why)
		return Batch[K]{}
	}
}

// until returns the first batch that satisfies ok.
func (r *recorder[K]) until(t *testing.T, why string, ok func(Batch[K]) bool) Batch[K] {
	t.Helper()
	for {
		if b := r.next(t, why); ok(b) {
			return b
		}
	}
}

func (r *recorder[K]) none(t *testing.T, why string) {
	t.Helper()
	select {
	case b := <-r.batches:
		t.Fatalf("unexpected batch %+v: %s", b, why)
	case <-time.After(quietFor):
	}
}

func start[K comparable](t *testing.T, cfg Config[K]) *Watcher[K] {
	t.Helper()
	w, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if startErr := w.Start(t.Context()); startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() { stopWithin(t, w) })
	return w
}

func stopWithin[K comparable](t *testing.T, w *Watcher[K]) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- w.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Stop: %v", err)
		}
	case <-time.After(waitFor):
		t.Fatal("timed out waiting for Stop")
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, parts ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(parts...), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func hasKey(want string) func(Batch[string]) bool {
	return func(b Batch[string]) bool { return slices.Contains(b.Keys, want) }
}

func isResync[K comparable](b Batch[K]) bool { return b.Resync }

// sawAll reports true once every wanted key has appeared in some batch.
func sawAll(want ...string) func(Batch[string]) bool {
	seen := map[string]struct{}{}
	return func(b Batch[string]) bool {
		for _, k := range b.Keys {
			seen[k] = struct{}{}
		}
		for _, k := range want {
			if _, ok := seen[k]; !ok {
				return false
			}
		}
		return true
	}
}

// rewriteUntil writes dir/compose.yaml until a batch reports it, so a write
// that lands before a watch is restored is retried instead of lost. It
// reports whether any batch on the way had Resync set.
func rewriteUntil(t *testing.T, rec *recorder[string], dir, why string) bool {
	t.Helper()
	want := filepath.Join(dir, "compose.yaml")
	resync := false
	for deadline := time.Now().Add(waitFor); time.Now().Before(deadline); {
		write(t, dir, "compose.yaml")
		select {
		case b := <-rec.batches:
			resync = resync || b.Resync
			if slices.Contains(b.Keys, want) {
				return resync
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("timed out: %s", why)
	return false
}

func TestNewValidates(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		want  error
		key   func(Event) (string, bool)
		name  string
		roots []string
	}{
		{name: "no roots", key: PathOf, want: ErrNoRoot},
		{name: "empty path", key: PathOf, roots: []string{dir, ""}, want: ErrNoRoot},
		{name: "nested", key: PathOf, roots: []string{dir, filepath.Join(dir, "sub")}, want: ErrNestedRoots},
		{name: "nil key", roots: []string{dir}, want: ErrNoKey},
		{name: "duplicates", key: PathOf, roots: []string{dir, dir + "/", filepath.Join(dir, "x", "..")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := New(Config[string]{Roots: tt.roots, Key: tt.key})
			if !errors.Is(err, tt.want) {
				t.Fatalf("New: got %v, want %v", err, tt.want)
			}
			if err == nil && !slices.Equal(w.roots, []string{dir}) {
				t.Errorf("roots = %q, want [%s]", w.roots, dir)
			}
		})
	}
}

func TestNestedRootsErrorNamesBothPaths(t *testing.T) {
	_, err := New(Config[string]{Roots: []string{"/srv/a", "/srv/a/b"}, Key: PathOf})
	if !errors.Is(err, ErrNestedRoots) || !strings.Contains(err.Error(), "/srv/a/b") || !strings.Contains(err.Error(), "/srv/a ") {
		t.Errorf("got %v, want ErrNestedRoots naming both paths", err)
	}
}

func TestStartFailsOnMissingRootAndIsSpent(t *testing.T) {
	w, err := New(Config[string]{
		Roots: []string{filepath.Join(t.TempDir(), "missing")},
		Key:   PathOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if startErr := w.Start(t.Context()); startErr == nil {
		t.Fatal("Start on missing root succeeded")
	}
	if startErr := w.Start(t.Context()); !errors.Is(startErr, ErrStopped) {
		t.Errorf("second Start: got %v, want ErrStopped", startErr)
	}
	stopWithin(t, w)
}

func TestWatchesExistingSymlinkDirectoriesWhenEnabled(t *testing.T) {
	root := t.TempDir()
	target := mkdir(t, t.TempDir(), "real-project")
	symlink(t, target, filepath.Join(root, "linked-project"))

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:          []string{root},
		Key:            composeKey,
		OnBatch:        rec.onBatch,
		Debounce:       fastDebounce,
		MaxDepth:       1,
		FollowSymlinks: true,
	})

	write(t, target, "compose.yaml")
	rec.until(t, "symlinked directory change should report the logical path",
		hasKey(filepath.Join(root, "linked-project", "compose.yaml")))
}

func TestWatchesNewDirectoriesInsideSymlinkDirectoriesWhenEnabled(t *testing.T) {
	root := t.TempDir()
	target := mkdir(t, t.TempDir(), "real-project")
	symlink(t, target, filepath.Join(root, "linked-project"))

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:          []string{root},
		Key:            composeKey,
		OnBatch:        rec.onBatch,
		Debounce:       fastDebounce,
		MaxDepth:       2,
		FollowSymlinks: true,
	})

	nested := mkdir(t, target, "nested-project")
	time.Sleep(100 * time.Millisecond) // let the new directory's watch attach
	write(t, nested, "compose.yaml")
	rec.until(t, "compose file in new symlinked directory should be reported",
		hasKey(filepath.Join(root, "linked-project", "nested-project", "compose.yaml")))
}

func TestIgnoresNonProjectFileWritesInsideProjectDir(t *testing.T) {
	root := t.TempDir()
	build := mkdir(t, root, "demo", ".esphome", "build", "node-1", ".piolibdeps", "node-1", "ESPMicroSpeechFeatures")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
	})

	for range 5 {
		if err := os.WriteFile(filepath.Join(build, "artifact.bin"), []byte{1, 2, 3}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec.none(t, "non-project writes must not trigger a batch")
}

func TestIgnoresChmodOnNonProjectFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(mkdir(t, root, "demo"), "README.md")
	if err := os.WriteFile(file, []byte("demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		MaxDepth: 1,
	})

	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	rec.none(t, "chmod on a non-project file must not trigger a batch")
}

func TestFiresOnComposeFileDroppedIntoNewlyCreatedDirectory(t *testing.T) {
	root := t.TempDir()

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
	})

	project := mkdir(t, root, "new-project")
	rec.none(t, "a plain directory create must not trigger a batch")

	write(t, project, "compose.yaml")
	rec.next(t, "compose file inside a new directory should trigger a batch")
}

func TestCatchesFilesInsideMovedInDirectory(t *testing.T) {
	root := t.TempDir()

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
	})

	// The tree is populated before it enters the watched root, so the only
	// way to see the file is the walk of the new directory.
	staging := mkdir(t, t.TempDir(), "fresh", "deep")
	write(t, staging, "compose.yaml")
	if err := os.Rename(filepath.Dir(staging), filepath.Join(root, "fresh")); err != nil {
		t.Fatal(err)
	}

	rec.until(t, "file inside a moved-in directory should be reported",
		hasKey(filepath.Join(root, "fresh", "deep", "compose.yaml")))
}

func TestSkipsExistingSymlinkDirectoriesWhenDisabled(t *testing.T) {
	root := t.TempDir()
	target := mkdir(t, t.TempDir(), "real-project")
	symlink(t, target, filepath.Join(root, "linked-project"))

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		MaxDepth: 1,
	})

	write(t, target, "compose.yaml")
	rec.none(t, "symlinked directory must not be watched when FollowSymlinks is off")
}

func TestTriggersOnNestedProjectFileBeyondDepthOne(t *testing.T) {
	root := t.TempDir()
	nested := mkdir(t, root, "main-project", "sub-project1")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
	})

	write(t, nested, "compose.yaml")
	rec.next(t, "nested compose file should trigger a batch with unlimited depth")
}

func TestDoesNotTriggerBeyondConfiguredDepth(t *testing.T) {
	root := t.TempDir()
	nested := mkdir(t, root, "main-project", "sub-project1")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		MaxDepth: 1,
	})

	write(t, nested, "compose.yaml")
	rec.none(t, "compose file beyond MaxDepth must not trigger a batch")
}

func TestTriggersOnDirectChildAtConfiguredDepth(t *testing.T) {
	root := t.TempDir()
	project := mkdir(t, root, "main-project")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		MaxDepth: 1,
	})

	write(t, project, "compose.yaml")
	rec.next(t, "compose file at MaxDepth should trigger a batch")
}

func TestIgnoresBareNestedDirectoryCreateAndRemove(t *testing.T) {
	root := t.TempDir()

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
	})

	mkdir(t, root, "main-project", "sub-project1")
	rec.none(t, "plain nested directory create must not trigger a batch")

	if err := os.RemoveAll(filepath.Join(root, "main-project")); err != nil {
		t.Fatal(err)
	}
	rec.none(t, "plain nested directory removal must not trigger a batch")
}

func TestSkipDirIsNotWatched(t *testing.T) {
	root := t.TempDir()
	trash := mkdir(t, root, ".arcane-trash-demo")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		SkipDir:  func(name string) bool { return name == ".arcane-trash-demo" },
		Debounce: fastDebounce,
	})

	write(t, trash, "compose.yaml")
	rec.none(t, "files in a skipped directory must not trigger a batch")
}

func TestStopIsIdempotentAfterStart(t *testing.T) {
	w, err := New(Config[string]{Roots: []string{t.TempDir()}, Key: PathOf})
	if err != nil {
		t.Fatal(err)
	}
	if startErr := w.Start(t.Context()); startErr != nil {
		t.Fatal(err)
	}
	stopWithin(t, w)
	stopWithin(t, w)
	if startErr := w.Start(t.Context()); !errors.Is(startErr, ErrStopped) {
		t.Errorf("Start after Stop: got %v, want ErrStopped", startErr)
	}
}

func TestStopIsSafeBeforeStart(t *testing.T) {
	w, err := New(Config[string]{Roots: []string{t.TempDir()}, Key: PathOf})
	if err != nil {
		t.Fatal(err)
	}
	stopWithin(t, w)
	stopWithin(t, w)
}

func TestMaxDelayFlushesUnderContinuousWrites(t *testing.T) {
	root := t.TempDir()

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: 200 * time.Millisecond,
		MaxDelay: 300 * time.Millisecond,
	})

	// Write faster than Debounce for well past MaxDelay. Without MaxDelay the
	// debounce would keep resetting and nothing would be delivered.
	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("x"), 0o644)
			}
		}
	}()
	defer func() {
		close(stop)
		<-writerDone
	}()

	select {
	case <-rec.batches:
	case <-time.After(time.Second):
		t.Fatal("MaxDelay did not flush a batch during continuous writes")
	}
}

func TestSignalCoalescesBurstIntoOneBatch(t *testing.T) {
	root := t.TempDir()

	var calls atomic.Int32
	rec := &recorder[struct{}]{batches: make(chan Batch[struct{}], 64)}
	start(t, Config[struct{}]{
		Roots: []string{root},
		Key:   Signal,
		OnBatch: func(ctx context.Context, b Batch[struct{}]) {
			calls.Add(1)
			rec.onBatch(ctx, b)
		},
		Debounce: 100 * time.Millisecond,
	})

	for i := range 20 {
		write(t, root, "file-"+strconv.Itoa(i)+".yaml")
	}

	if b := rec.next(t, "burst should produce a batch"); len(b.Keys) != 1 {
		t.Errorf("got %d keys, want 1", len(b.Keys))
	}
	rec.none(t, "burst should produce exactly one batch")
	if got := calls.Load(); got != 1 {
		t.Errorf("OnBatch called %d times, want 1", got)
	}
}

func TestPanickingOnBatchDoesNotStopDelivery(t *testing.T) {
	root := t.TempDir()

	var calls atomic.Int32
	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots: []string{root},
		Key:   composeKey,
		OnBatch: func(ctx context.Context, b Batch[string]) {
			if calls.Add(1) == 1 {
				panic("boom")
			}
			rec.onBatch(ctx, b)
		},
		Debounce: fastDebounce,
	})

	write(t, root, "compose.yaml")
	deadline := time.Now().Add(waitFor)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first batch never delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	write(t, root, "docker-compose.yml")
	rec.next(t, "delivery should survive a panicking OnBatch")
}

func TestRootRemovedAndRecreatedResyncs(t *testing.T) {
	root := mkdir(t, t.TempDir(), "projects")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{root},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		Backoff:  fastBackoff,
	})

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	rec.until(t, "losing the root should emit a resync batch", isResync)

	if !rewriteUntil(t, rec, mkdir(t, root, "demo"), "recreated root should be watched") {
		t.Error("restoring the root should emit a resync batch")
	}
}

func TestOverflowRequestsResync(t *testing.T) {
	w, err := New(Config[string]{Roots: []string{t.TempDir()}, Key: PathOf})
	if err != nil {
		t.Fatal(err)
	}
	p := &pending[string]{keys: map[string]struct{}{}}
	if !w.handleErr(t.Context(), fsnotify.ErrEventOverflow, p) || !p.resync {
		t.Fatal("overflow did not request a resync")
	}
	other := &pending[string]{keys: map[string]struct{}{}}
	if w.handleErr(t.Context(), errors.New("boom"), other) || other.resync {
		t.Fatal("a generic error must not request a resync")
	}
}

func TestSymlinkCycleTerminates(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "a")
	symlink(t, root, filepath.Join(a, "loop"))

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:          []string{root},
		Key:            composeKey,
		OnBatch:        rec.onBatch,
		Debounce:       fastDebounce,
		FollowSymlinks: true,
	})

	write(t, a, "compose.yaml")
	rec.until(t, "a cycle must not re-alias the root", hasKey(filepath.Join(a, "compose.yaml")))
}

func TestStopDuringEventStorm(t *testing.T) {
	root := t.TempDir()
	w := start(t, Config[string]{
		Roots:    []string{root},
		Key:      PathOf,
		OnBatch:  func(context.Context, Batch[string]) { time.Sleep(time.Millisecond) },
		Debounce: time.Millisecond,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 2000 {
			dir := filepath.Join(root, "d", strconv.Itoa(i%26))
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	stopWithin(t, w)
	<-done
}

func TestChangesAcrossRootsShareOneBatch(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{a, b},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: 200 * time.Millisecond,
	})

	write(t, a, "compose.yaml")
	write(t, b, "compose.yaml")

	got := rec.next(t, "changes in both roots should produce a batch")
	want := []string{filepath.Join(a, "compose.yaml"), filepath.Join(b, "compose.yaml")}
	slices.Sort(got.Keys)
	slices.Sort(want)
	if !slices.Equal(got.Keys, want) {
		t.Errorf("keys = %q, want %q in one batch", got.Keys, want)
	}
}

func TestEventRootAndRel(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	target := mkdir(t, t.TempDir(), "real")
	mkdir(t, a, "x")
	mkdir(t, b, "y", "z")
	symlink(t, target, filepath.Join(b, "link"))

	rec := &recorder[Event]{batches: make(chan Batch[Event], 64)}
	start(t, Config[Event]{
		Roots: []string{a, b},
		// Op is dropped so repeated operations on one path match a single want.
		Key: func(e Event) (Event, bool) {
			return Event{
				Path:    e.Path,
				Logical: e.Logical,
				Root:    e.Root,
				Rel:     e.Rel,
			}, true
		},
		OnBatch:        rec.onBatch,
		Debounce:       fastDebounce,
		FollowSymlinks: true,
	})

	tests := []struct {
		change func(t *testing.T)
		name   string
		want   Event
	}{
		{
			name:   "root itself",
			change: func(t *testing.T) { chmod(t, a, 0o750) },
			want: Event{
				Path:    a,
				Logical: a,
				Root:    a,
				Rel:     ".",
			},
		},
		{
			name:   "first root",
			change: func(t *testing.T) { write(t, a, "x", "compose.yaml") },
			want: Event{
				Path:    filepath.Join(a, "x", "compose.yaml"),
				Logical: filepath.Join(a, "x", "compose.yaml"),
				Root:    a,
				Rel:     filepath.Join("x", "compose.yaml"),
			},
		},
		{
			name:   "second root nested",
			change: func(t *testing.T) { write(t, b, "y", "z", "compose.yaml") },
			want: Event{
				Path:    filepath.Join(b, "y", "z", "compose.yaml"),
				Logical: filepath.Join(b, "y", "z", "compose.yaml"),
				Root:    b,
				Rel:     filepath.Join("y", "z", "compose.yaml"),
			},
		},
		{
			name:   "followed symlink",
			change: func(t *testing.T) { write(t, target, "compose.yaml") },
			want: Event{
				Path:    filepath.Join(target, "compose.yaml"),
				Logical: filepath.Join(b, "link", "compose.yaml"),
				Root:    b,
				Rel:     filepath.Join("link", "compose.yaml"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.change(t)
			rec.until(t, "event should carry its root and relative path", func(b Batch[Event]) bool {
				return slices.Contains(b.Keys, tt.want)
			})
		})
	}
}

func TestMaxDepthAppliesPerRoot(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, root := range []string{a, b} {
		mkdir(t, root, "p", "q")
	}

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{a, b},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		MaxDepth: 1,
	})

	write(t, a, "p", "q", "compose.yaml")
	write(t, b, "p", "q", "compose.yaml")
	rec.none(t, "files beyond MaxDepth in any root must not trigger a batch")

	write(t, a, "p", "compose.yaml")
	write(t, b, "p", "compose.yaml")
	rec.until(t, "files at MaxDepth in every root should be reported",
		sawAll(filepath.Join(a, "p", "compose.yaml"), filepath.Join(b, "p", "compose.yaml")))
}

func TestStartFailsWhenOneRootIsMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	w, err := New(Config[string]{
		Roots: []string{t.TempDir(), missing},
		Key:   PathOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if startErr := w.Start(t.Context()); startErr == nil || !strings.Contains(startErr.Error(), missing) {
		t.Fatalf("Start: got %v, want an error naming %s", startErr, missing)
	}
	if startErr := w.Start(t.Context()); !errors.Is(startErr, ErrStopped) {
		t.Errorf("second Start: got %v, want ErrStopped", startErr)
	}
	stopWithin(t, w)
}

func TestLosingOneRootKeepsOthersWatched(t *testing.T) {
	tests := []struct {
		// lose takes root away and returns where its demo dir ended up, or ""
		// if it no longer exists.
		lose func(t *testing.T, root string) string
		name string
	}{
		{
			name: "removed",
			lose: func(t *testing.T, root string) string {
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
				return ""
			},
		},
		{
			name: "renamed",
			lose: func(t *testing.T, root string) string {
				if err := os.Rename(root, root+".old"); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(root+".old", "demo")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			a, b := mkdir(t, parent, "a"), mkdir(t, parent, "b")
			mkdir(t, a, "demo")

			rec := &recorder[string]{batches: make(chan Batch[string], 64)}
			start(t, Config[string]{
				Roots:    []string{a, b},
				Key:      composeKey,
				OnBatch:  rec.onBatch,
				Debounce: fastDebounce,
				Backoff:  fastBackoff,
			})

			if moved := tt.lose(t, a); moved != "" {
				write(t, moved, "compose.yaml")
			}
			write(t, b, "compose.yaml")
			stale := filepath.Join(a, "demo", "compose.yaml")
			resync := false
			rec.until(t, "the remaining root should keep delivering", func(batch Batch[string]) bool {
				if slices.Contains(batch.Keys, stale) {
					t.Errorf("lost root reported %s after it moved away", stale)
				}
				resync = resync || batch.Resync
				return slices.Contains(batch.Keys, filepath.Join(b, "compose.yaml"))
			})
			if !resync {
				t.Error("losing a root should emit a resync batch")
			}

			if !rewriteUntil(t, rec, mkdir(t, a, "demo"), "recreated root should be watched") {
				t.Error("restoring a root should emit a resync batch")
			}
			write(t, b, "docker-compose.yml")
			rec.until(t, "the other root should still deliver after the restore",
				hasKey(filepath.Join(b, "docker-compose.yml")))
		})
	}
}

func TestSharedSymlinkTargetPrefersEarlierRoot(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	target := mkdir(t, t.TempDir(), "shared")
	symlink(t, target, filepath.Join(a, "link"))
	symlink(t, target, filepath.Join(b, "link"))

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:          []string{b, a},
		Key:            composeKey,
		OnBatch:        rec.onBatch,
		Debounce:       fastDebounce,
		FollowSymlinks: true,
	})

	write(t, target, "compose.yaml")
	got := rec.until(t, "a change in a shared target should be reported once",
		hasKey(filepath.Join(b, "link", "compose.yaml")))
	if slices.Contains(got.Keys, filepath.Join(a, "link", "compose.yaml")) {
		t.Errorf("keys = %q, want only the earlier root's link", got.Keys)
	}

	if err := os.Remove(filepath.Join(b, "link")); err != nil {
		t.Fatal(err)
	}
	write(t, target, "compose.yaml")
	rec.until(t, "the remaining link should take over the shared target",
		hasKey(filepath.Join(a, "link", "compose.yaml")))
}

func TestStopWithMultipleRootsLeavesNoGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	parent := t.TempDir()
	a, b := mkdir(t, parent, "a"), mkdir(t, parent, "b")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	w, err := New(Config[string]{
		Roots:    []string{a, b},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		Backoff:  fastBackoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if startErr := w.Start(t.Context()); startErr != nil {
		t.Fatal(startErr)
	}
	if removeErr := os.RemoveAll(a); removeErr != nil {
		t.Fatal(removeErr)
	}
	rec.until(t, "losing a root should emit a resync batch", isResync)
	stopWithin(t, w)

	for deadline := time.Now().Add(waitFor); runtime.NumGoroutine() > before; {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d after Stop, %d before Start", runtime.NumGoroutine(), before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// rootRelKey keys compose files by the root and relative path they are
// reported under.
func rootRelKey(e Event) (string, bool) {
	return e.Root + "|" + e.Rel, filepath.Base(e.Path) == "compose.yaml"
}

func TestSymlinkToAnotherRootsTreeIsNotFollowed(t *testing.T) {
	tests := []struct {
		// setup builds the roots (in Config.Roots order) and returns them with
		// the directory to write in and the key that write must produce.
		setup func(t *testing.T, parent string) (roots []string, dir, want string)
		name  string
	}{
		{
			name: "link to an ancestor of another root",
			setup: func(t *testing.T, parent string) ([]string, string, string) {
				r1 := mkdir(t, parent, "outer", "r1")
				r2 := mkdir(t, parent, "r2")
				symlink(t, filepath.Join(parent, "outer"), filepath.Join(r2, "up"))
				return []string{r1, r2}, mkdir(t, r1, "proj"), r1 + "|" + filepath.Join("proj", "compose.yaml")
			},
		},
		{
			name: "absolute link into a later relative root",
			setup: func(t *testing.T, parent string) ([]string, string, string) {
				t.Chdir(parent)
				sub := mkdir(t, parent, "a", "sub")
				mkdir(t, parent, "b")
				symlink(t, sub, filepath.Join(parent, "b", "l"))
				return []string{"b", "a"}, sub, "a|" + filepath.Join("sub", "compose.yaml")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots, dir, want := tt.setup(t, t.TempDir())

			rec := &recorder[string]{batches: make(chan Batch[string], 64)}
			start(t, Config[string]{
				Roots:          roots,
				Key:            rootRelKey,
				OnBatch:        rec.onBatch,
				Debounce:       fastDebounce,
				FollowSymlinks: true,
			})

			write(t, dir, "compose.yaml")
			got := rec.next(t, "a change under a root should be reported")
			if !slices.Equal(got.Keys, []string{want}) {
				t.Errorf("keys = %q, want only %q", got.Keys, want)
			}
		})
	}
}

func TestSharedSymlinkTargetRespectsMaxDepth(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	target := mkdir(t, t.TempDir(), "shared")
	sub := mkdir(t, target, "sub")
	symlink(t, target, filepath.Join(mkdir(t, r1, "x"), "l"))
	symlink(t, target, filepath.Join(r2, "l"))

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:          []string{r1, r2},
		Key:            rootRelKey,
		OnBatch:        rec.onBatch,
		Debounce:       fastDebounce,
		MaxDepth:       2,
		FollowSymlinks: true,
	})

	write(t, sub, "compose.yaml")
	got := rec.next(t, "the shallower link should report what the deeper one cannot")
	if want := r2 + "|" + filepath.Join("l", "sub", "compose.yaml"); !slices.Equal(got.Keys, []string{want}) {
		t.Errorf("keys = %q, want only %q", got.Keys, want)
	}

	write(t, target, "compose.yaml")
	got = rec.next(t, "the earlier root should report what is within its depth")
	if want := r1 + "|" + filepath.Join("x", "l", "compose.yaml"); !slices.Equal(got.Keys, []string{want}) {
		t.Errorf("keys = %q, want only %q", got.Keys, want)
	}
}

func TestStartFailsWhenRootIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	write(t, file)
	w, err := New(Config[string]{
		Roots: []string{t.TempDir(), file},
		Key:   PathOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if startErr := w.Start(t.Context()); startErr == nil || !strings.Contains(startErr.Error(), file) {
		t.Fatalf("Start: got %v, want an error naming %s", startErr, file)
	}
	stopWithin(t, w)
}

func TestRootReplacedByFileIsLostUntilItIsADirectoryAgain(t *testing.T) {
	parent := t.TempDir()
	a, b := mkdir(t, parent, "a"), mkdir(t, parent, "b")

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, Config[string]{
		Roots:    []string{a, b},
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		Backoff:  fastBackoff,
	})

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	write(t, a)
	rec.until(t, "losing a root should emit a resync batch", isResync)
	write(t, b, "compose.yaml")
	rec.until(t, "the other root should keep delivering", hasKey(filepath.Join(b, "compose.yaml")))

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if !rewriteUntil(t, rec, mkdir(t, a, "demo"), "root should be watched once it is a directory again") {
		t.Error("restoring a root should emit a resync batch")
	}
}
