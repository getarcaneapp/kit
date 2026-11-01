package tripwire

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

const sep = string(os.PathSeparator)

// tree tracks which directories are subscribed and how real paths map back
// to logical paths under the root. It is owned by one goroutine at a time and
// is not safe for concurrent use.
type tree struct {
	// aliases maps a followed symlink target (real dir) to the logical dir
	// where the link lives. Only symlink points are stored; everything below
	// them resolves by walking up to the nearest alias.
	aliases  map[string]string
	skip     func(name string) bool
	log      *slog.Logger
	root     string
	prefix   string // root with a trailing separator
	maxDepth int
	follow   bool
}

// frame is one directory waiting to be walked.
type frame struct {
	dir     string
	logical string
	depth   int
	// link marks a followed symlink target; it becomes an alias once the
	// walk knows it isn't a cycle.
	link bool
}

// emitFunc receives the non-directory entries found while walking a newly
// created directory, so files written before the watch landed aren't lost.
type emitFunc func(realPath, logical string)

// logical maps a real path to its logical path under the root. It costs one
// map lookup per path component, independent of how many dirs are watched.
func (t *tree) logical(realPath string) string {
	if len(t.aliases) == 0 {
		return realPath
	}
	for dir := realPath; ; {
		if logical, ok := t.aliases[dir]; ok {
			return logical + realPath[len(dir):]
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return realPath
		}
		dir = parent
	}
}

// rel returns logical relative to the root. Paths outside the root start
// with "..".
func (t *tree) rel(logical string) string {
	if logical == t.root {
		return "."
	}
	if strings.HasPrefix(logical, t.prefix) {
		return logical[len(t.prefix):]
	}
	if rel, err := filepath.Rel(t.root, logical); err == nil {
		return rel
	}
	return ".."
}

// depth returns how many levels below the root rel is: 0 for the root and
// -1 for paths outside it.
func depth(rel string) int {
	switch {
	case rel == ".":
		return 0
	case rel == ".." || strings.HasPrefix(rel, ".."+sep):
		return -1
	default:
		return strings.Count(rel, sep) + 1
	}
}

// attach subscribes a newly created directory (and everything below it) if it
// is inside the depth limit. Entries already inside it are passed to emit.
func (t *tree) attach(ctx context.Context, fsw *fsnotify.Watcher, realPath, logical string, emit emitFunc) {
	d := depth(t.rel(logical))
	if d <= 0 || (t.maxDepth > 0 && d > t.maxDepth) {
		return
	}
	target, ok := t.dirTarget(realPath)
	if !ok {
		return
	}
	start := frame{
		dir:     target,
		logical: logical,
		depth:   d,
		link:    target != realPath,
	}
	if err := t.walk(ctx, fsw, start, emit); err != nil {
		t.log.WarnContext(ctx, "tripwire: failed to watch new directory", "path", realPath, "error", err)
	}
}

// dirTarget reports whether path is a directory to watch and returns the
// path to subscribe: path itself, or its resolved target for a followed
// symlink.
func (t *tree) dirTarget(path string) (string, bool) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return "", false
	case info.IsDir():
		return path, true
	case !t.follow || info.Mode()&fs.ModeSymlink == 0:
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	info, err = os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return resolved, true
}

// walk subscribes start and every directory below it, iteratively. The start
// frame itself is subscribed only when it is below the root; the caller owns
// the root subscription. Only an unreadable root is returned as an error.
func (t *tree) walk(ctx context.Context, fsw *fsnotify.Watcher, start frame, emit emitFunc) error {
	visited := map[fileID]struct{}{}
	stack := []frame{start}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if t.follow && t.seen(visited, f.dir) {
			continue
		}
		if f.link {
			t.aliases[f.dir] = f.logical
		}
		if f.depth > 0 {
			t.watch(ctx, fsw, f.dir)
		}

		leaf := t.maxDepth > 0 && f.depth >= t.maxDepth
		if leaf && emit == nil {
			continue
		}
		entries, err := os.ReadDir(f.dir)
		if err != nil {
			if f.depth == 0 {
				return err
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.log.WarnContext(ctx, "tripwire: skipping unreadable directory", "path", f.dir, "error", err)
			}
			continue
		}
		for _, entry := range entries {
			if next, ok := t.child(f, entry, leaf, emit); ok {
				stack = append(stack, next)
			}
		}
	}
	return nil
}

// child classifies one directory entry. It returns a frame to walk for
// subdirectories, and passes everything else to emit.
func (t *tree) child(parent frame, entry fs.DirEntry, leaf bool, emit emitFunc) (frame, bool) {
	name := entry.Name()
	realPath := parent.dir + sep + name
	logical := parent.logical + sep + name

	switch {
	case entry.IsDir():
		return frame{dir: realPath, logical: logical, depth: parent.depth + 1}, !leaf && !t.skip(name)
	case t.follow && entry.Type()&fs.ModeSymlink != 0:
		if target, ok := t.dirTarget(realPath); ok {
			if leaf || t.skip(name) {
				return frame{}, false
			}
			return frame{
				dir:     target,
				logical: logical,
				depth:   parent.depth + 1,
				link:    true,
			}, true
		}
	}
	if emit != nil {
		emit(realPath, logical)
	}
	return frame{}, false
}

// seen records the directory's identity and reports whether it was already
// walked, which breaks symlink cycles.
func (t *tree) seen(visited map[fileID]struct{}, path string) bool {
	id, err := identify(path)
	if err != nil {
		return true
	}
	if _, ok := visited[id]; ok {
		return true
	}
	visited[id] = struct{}{}
	return false
}

func (t *tree) watch(ctx context.Context, fsw *fsnotify.Watcher, path string) {
	if err := fsw.Add(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.log.WarnContext(ctx, "tripwire: failed to watch directory", "path", path, "error", err)
	}
}

// forget drops followed symlinks at or below a removed logical path, and the
// subscriptions on their targets. Plain directories need nothing: the OS
// drops a watch when its directory goes away.
func (t *tree) forget(fsw *fsnotify.Watcher, logical string) {
	if len(t.aliases) == 0 {
		return
	}
	var targets []string
	for target, link := range t.aliases {
		if link == logical || strings.HasPrefix(link, logical+sep) {
			delete(t.aliases, target)
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return
	}
	for _, watched := range fsw.WatchList() {
		for _, target := range targets {
			if watched == target || strings.HasPrefix(watched, target+sep) {
				_ = fsw.Remove(watched) // already gone is fine
				break
			}
		}
	}
}
