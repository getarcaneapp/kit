package tripwire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

func start[K comparable](t *testing.T, root string, cfg Config[K]) *Watcher[K] {
	t.Helper()
	w, err := New(root, cfg)
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

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func hasKey(want string) func(Batch[string]) bool {
	return func(b Batch[string]) bool { return slices.Contains(b.Keys, want) }
}

func TestNewValidates(t *testing.T) {
	if _, err := New("", Config[string]{Key: PathOf}); !errors.Is(err, ErrNoRoot) {
		t.Errorf("empty root: got %v, want ErrNoRoot", err)
	}
	if _, err := New(t.TempDir(), Config[string]{}); !errors.Is(err, ErrNoKey) {
		t.Errorf("nil key: got %v, want ErrNoKey", err)
	}
}

func TestStartFailsOnMissingRootAndIsSpent(t *testing.T) {
	w, err := New(filepath.Join(t.TempDir(), "missing"), Config[string]{Key: PathOf})
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
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
	start(t, root, Config[string]{
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		SkipDir:  func(name string) bool { return name == ".arcane-trash-demo" },
		Debounce: fastDebounce,
	})

	write(t, trash, "compose.yaml")
	rec.none(t, "files in a skipped directory must not trigger a batch")
}

func TestStopIsIdempotentAfterStart(t *testing.T) {
	w, err := New(t.TempDir(), Config[string]{Key: PathOf})
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
	w, err := New(t.TempDir(), Config[string]{Key: PathOf})
	if err != nil {
		t.Fatal(err)
	}
	stopWithin(t, w)
	stopWithin(t, w)
}

func TestMaxDelayFlushesUnderContinuousWrites(t *testing.T) {
	root := t.TempDir()

	rec := &recorder[string]{batches: make(chan Batch[string], 64)}
	start(t, root, Config[string]{
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
	start(t, root, Config[struct{}]{
		Key: Signal,
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
	start(t, root, Config[string]{
		Key: composeKey,
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
	start(t, root, Config[string]{
		Key:      composeKey,
		OnBatch:  rec.onBatch,
		Debounce: fastDebounce,
		Backoff: Backoff{
			Initial: 20 * time.Millisecond,
			Max:     50 * time.Millisecond,
		},
	})

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	demo := mkdir(t, root, "demo")

	rec.until(t, "reconnect should emit a resync batch", func(b Batch[string]) bool { return b.Resync })

	write(t, demo, "compose.yaml")
	rec.until(t, "recreated root should be watched", hasKey(filepath.Join(demo, "compose.yaml")))
}

func TestOverflowRequestsResync(t *testing.T) {
	w, err := New(t.TempDir(), Config[string]{Key: PathOf})
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
	start(t, root, Config[string]{
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
	w := start(t, root, Config[string]{
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
