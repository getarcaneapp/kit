package tripwire

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type (
	// tree tracks which directories are subscribed and how real paths map back
	// to logical paths under the roots. It is owned by one goroutine at a time
	// and is not safe for concurrent use.
	tree struct {
		// aliases maps a followed symlink target (real dir) to the logical dirs
		// where links to it live, ordered by root precedence. The first entry
		// wins; the next takes over when it is forgotten. Only symlink points
		// are stored; everything below them resolves by walking up to the
		// nearest alias.
		aliases map[string][]string
		// lost holds roots whose subscription is gone and is being retried.
		lost map[string]struct{}
		// watched holds every subscribed directory, sorted, so a moved or
		// removed subtree can be released without listing all watches.
		watched  []string
		skip     func(name string) bool
		log      *slog.Logger
		roots    []rootDir
		maxDepth int
		follow   bool
	}

	// rootDir is one watched root.
	rootDir struct {
		path   string
		prefix string // path with a trailing separator
		// real is path made absolute with symlinks resolved, set each time the
		// root is subscribed. Symlink targets are compared against it.
		real string
	}

	// frame is one directory waiting to be walked.
	frame struct {
		dir     string
		logical string
		depth   int
		// link marks a followed symlink target; it becomes an alias once the
		// walk knows it isn't a cycle.
		link bool
	}

	// emitFunc receives the non-directory entries found while walking a newly
	// created directory, so files written before the watch landed aren't lost.
	emitFunc func(realPath, logical string)
)

const (
	sep = string(os.PathSeparator)
)

// newTree builds an empty tree for already validated roots and a resolved
// config.
func newTree[K comparable](roots []string, cfg Config[K]) *tree {
	dirs := make([]rootDir, 0, len(roots))
	for _, root := range roots {
		dirs = append(dirs, rootDir{path: root, prefix: withSep(root)})
	}
	return &tree{
		aliases:  make(map[string][]string),
		lost:     make(map[string]struct{}),
		skip:     cfg.SkipDir,
		log:      cfg.Logger,
		roots:    dirs,
		maxDepth: cfg.MaxDepth,
		follow:   cfg.FollowSymlinks,
	}
}

// withSep returns path with exactly one trailing separator.
func withSep(path string) string {
	return strings.TrimSuffix(path, sep) + sep
}

// logical maps a real path to its logical path under a root. It costs one
// map lookup per path component, independent of how many dirs are watched.
// When several links share a target, the first one that keeps the path within
// the depth limit is used, so a deeper link under an earlier root does not
// hide events a shallower link is entitled to.
func (t *tree) logical(realPath string) string {
	if len(t.aliases) == 0 {
		return realPath
	}
	for dir := realPath; ; {
		if links, ok := t.aliases[dir]; ok {
			for _, link := range links {
				if _, rel := t.locate(link + realPath[len(dir):]); t.maxDepth == 0 || depth(rel) <= t.maxDepth+1 {
					return link + realPath[len(dir):]
				}
			}
			return links[0] + realPath[len(dir):]
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return realPath
		}
		dir = parent
	}
}

// locate returns the index of the root containing logical and logical
// relative to that root. Paths outside every root return -1 and "..".
func (t *tree) locate(logical string) (int, string) {
	for i, root := range t.roots {
		switch {
		case logical == root.path:
			return i, "."
		case strings.HasPrefix(logical, root.prefix):
			return i, logical[len(root.prefix):]
		}
	}
	return -1, ".."
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

// add subscribes root i and every directory below it.
func (t *tree) add(ctx context.Context, fsw *fsnotify.Watcher, i int) error {
	if err := t.subscribe(fsw, i); err != nil {
		return err
	}
	return t.walkRoot(ctx, fsw, i)
}

// subscribe watches root i itself and records its resolved path.
func (t *tree) subscribe(fsw *fsnotify.Watcher, i int) error {
	root := t.roots[i]
	if err := fsw.Add(root.path); err != nil {
		return err
	}
	t.track(root.path)
	resolved, err := filepath.EvalSymlinks(root.path)
	if err != nil {
		t.unwatch(fsw, root.path)
		return err
	}
	full, err := filepath.Abs(resolved)
	if err != nil {
		t.unwatch(fsw, root.path)
		return err
	}
	t.roots[i] = rootDir{
		path:   root.path,
		prefix: root.prefix,
		real:   full,
	}
	return nil
}

// walkRoot watches every directory below the subscribed root i. A root that
// cannot be read as a directory is unsubscribed and its error returned.
func (t *tree) walkRoot(ctx context.Context, fsw *fsnotify.Watcher, i int) error {
	root := t.roots[i].path
	if err := t.walk(ctx, fsw, frame{dir: root, logical: root}, nil); err != nil {
		t.unwatch(fsw, root)
		return err
	}
	return nil
}

// detach drops every subscription and alias belonging to root, so a moved
// root's subtree stops reporting under its old path.
func (t *tree) detach(fsw *fsnotify.Watcher, root rootDir) {
	t.forget(fsw, root.path)
	t.unwatch(fsw, root.path)
}

// attach subscribes a newly created directory (and everything below it) if it
// is inside the depth limit. Entries already inside it are passed to emit.
func (t *tree) attach(ctx context.Context, fsw *fsnotify.Watcher, realPath, logical string, emit emitFunc) {
	_, rel := t.locate(logical)
	d := depth(rel)
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
// symlink. A symlink whose target is a root, lies inside one, or contains one
// is not followed: the root already watches those directories under its own
// path, and following would report its events under the link instead.
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
	full, err := filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	for _, root := range t.roots {
		if root.real != "" && (full == root.real ||
			strings.HasPrefix(full, withSep(root.real)) ||
			strings.HasPrefix(root.real, withSep(full))) {
			return "", false
		}
	}
	info, err = os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return resolved, true
}

// walk subscribes start and every directory below it, iteratively. The start
// frame itself is subscribed only when it is below a root; the caller owns
// the root subscription. Only an unreadable start directory at depth 0 or a
// done ctx is returned as an error.
func (t *tree) walk(ctx context.Context, fsw *fsnotify.Watcher, start frame, emit emitFunc) error {
	visited := map[fileID]struct{}{}
	stack := []frame{start}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if t.follow && t.seen(visited, f.dir) {
			continue
		}
		if f.link {
			t.alias(f.dir, f.logical)
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

// alias records that the symlink at logical points at target. A link under
// an earlier root in Config.Roots takes precedence; within one root the
// first link recorded wins.
func (t *tree) alias(target, logical string) {
	links := t.aliases[target]
	if slices.Contains(links, logical) {
		return
	}
	links = append(links, logical)
	slices.SortStableFunc(links, func(a, b string) int {
		ia, _ := t.locate(a)
		ib, _ := t.locate(b)
		return cmp.Compare(ia, ib)
	})
	t.aliases[target] = links
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
	if err := fsw.Add(path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			t.log.WarnContext(ctx, "tripwire: failed to watch directory", "path", path, "error", err)
		}
		return
	}
	t.track(path)
}

// track records a subscribed directory in the sorted watched set.
func (t *tree) track(path string) {
	if i, found := slices.BinarySearch(t.watched, path); !found {
		t.watched = slices.Insert(t.watched, i, path)
	}
}

// unwatch drops the subscriptions on path and every directory below it. The
// OS releases a watch only on the directory that was removed or moved, so a
// renamed directory's children would otherwise keep reporting under the old
// path.
func (t *tree) unwatch(fsw *fsnotify.Watcher, path string) {
	prefix := withSep(path)
	from, _ := slices.BinarySearch(t.watched, prefix)
	to := from
	for to < len(t.watched) && strings.HasPrefix(t.watched[to], prefix) {
		_ = fsw.Remove(t.watched[to]) // already gone is fine
		to++
	}
	t.watched = slices.Delete(t.watched, from, to)
	if i, found := slices.BinarySearch(t.watched, path); found {
		_ = fsw.Remove(path) // already gone is fine
		t.watched = slices.Delete(t.watched, i, i+1)
	}
}

// forget drops followed symlinks at or below a removed logical path. A
// target's subscriptions are removed only once no link points at it any
// more. Subscriptions under the removed path itself are released by unwatch.
func (t *tree) forget(fsw *fsnotify.Watcher, logical string) {
	if len(t.aliases) == 0 {
		return
	}
	prefix := withSep(logical)
	gone := func(link string) bool { return link == logical || strings.HasPrefix(link, prefix) }
	var targets []string
	for target, links := range t.aliases {
		if !slices.ContainsFunc(links, gone) {
			continue
		}
		if kept := slices.DeleteFunc(links, gone); len(kept) > 0 {
			t.aliases[target] = kept
			continue
		}
		delete(t.aliases, target)
		targets = append(targets, target)
	}
	for _, target := range targets {
		t.unwatch(fsw, target)
	}
}
