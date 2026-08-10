package acfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"go.getarcane.app/acfs/pkg/utils"
	acfstypes "go.getarcane.app/acfs/types"
)

const directoryReadBatchSize = 256

func listNamesInternal(ctx context.Context, root *os.Root, resolvedPath, logicalPath string) ([]string, error) {
	directory, err := root.Open(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("open directory %q: %w", logicalPath, err)
	}
	defer func() { _ = directory.Close() }()

	names := make([]string, 0, directoryReadBatchSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := directory.ReadDir(directoryReadBatchSize)
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read directory %q: %w", logicalPath, readErr)
		}
	}
	slices.Sort(names)
	return names, nil
}

// ListEach visits the direct children of a root-confined directory in
// deterministic name order without retaining all metadata in memory.
func ListEach(ctx context.Context, rootPath, logicalPath string, visit func(acfstypes.Entry) error) error {
	if visit == nil {
		return errors.New("visit callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePathInternal(root, relativePath, true)
	if err != nil {
		return err
	}

	names, err := listNamesInternal(ctx, root, resolvedPath, logicalPath)
	if err != nil {
		return err
	}

	for _, name := range names {
		if strings.HasPrefix(name, temporaryWritePrefix) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		entryPath := name
		if resolvedPath != "." {
			entryPath = resolvedPath + "/" + name
		}
		info, err := root.Lstat(entryPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat %q: %w", utils.LogicalPath(entryPath), err)
		}

		entry, err := entryFromInfoInternal(root, entryPath, info)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if err := visit(entry); err != nil {
			return fmt.Errorf("visit %q: %w", entry.Path, err)
		}
	}
	return nil
}

// List returns the direct children of a root-confined directory.
func List(ctx context.Context, rootPath, logicalPath string) ([]acfstypes.Entry, error) {
	entries := make([]acfstypes.Entry, 0)
	err := ListEach(ctx, rootPath, logicalPath, func(entry acfstypes.Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Stat returns metadata for a root-confined path. When followFinal is false the
// final symbolic link is not followed, so the Entry describes the link itself.
// When it is true the link is resolved and the returned Entry.Path is the fully
// resolved logical path, making Stat double as a root-confined symlink resolver.
func Stat(ctx context.Context, rootPath, logicalPath string, followFinal bool) (acfstypes.Entry, error) {
	if err := ctx.Err(); err != nil {
		return acfstypes.Entry{}, err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return acfstypes.Entry{}, err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePathInternal(root, relativePath, followFinal)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	info, err := root.Lstat(resolvedPath)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("stat %q: %w", logicalPath, err)
	}

	return entryFromInfoInternal(root, resolvedPath, info)
}

// Exists reports whether a root-confined path exists without following its
// final symbolic link, so a dangling link still counts as existing. A target
// missing anywhere along the path is reported as (false, nil); every other
// failure — including a path that escapes the root — is returned as an error.
func Exists(ctx context.Context, rootPath, logicalPath string) (bool, error) {
	if _, err := Stat(ctx, rootPath, logicalPath, false); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Walk visits every descendant of a root-confined directory in deterministic
// depth-first order. It does not follow symbolic links encountered in the tree.
func Walk(ctx context.Context, rootPath, logicalPath string, visit func(acfstypes.Entry) error) error {
	_, err := WalkBounded(ctx, rootPath, logicalPath, acfstypes.WalkOptions{}, visit)
	return err
}

type walkDirectoryInternal struct {
	path  string
	depth int
}

type boundedWalkerInternal struct {
	ctx      context.Context
	rootPath string
	options  acfstypes.WalkOptions
	visit    func(acfstypes.Entry) error
	result   acfstypes.WalkResult
}

func directoryHasEntriesInternal(ctx context.Context, rootPath, logicalPath string) (bool, error) {
	found := false
	err := ListEach(ctx, rootPath, logicalPath, func(acfstypes.Entry) error {
		found = true
		return fs.SkipAll
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return false, err
	}
	return found, nil
}

func (w *boundedWalkerInternal) visitEntryInternal(current walkDirectoryInternal, childDirectories *[]walkDirectoryInternal, entry acfstypes.Entry) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.options.MaxEntries > 0 && w.result.Count >= w.options.MaxEntries {
		w.result.Truncated = true
		return fs.SkipAll
	}

	visitErr := w.visit(entry)
	if visitErr != nil && !errors.Is(visitErr, fs.SkipDir) && !errors.Is(visitErr, fs.SkipAll) {
		return visitErr
	}
	w.result.Count++
	if errors.Is(visitErr, fs.SkipAll) {
		return fs.SkipAll
	}
	if !entry.IsDirectory || entry.IsSymlink || errors.Is(visitErr, fs.SkipDir) {
		return nil
	}

	entryDepth := current.depth + 1
	if w.options.MaxDepth > 0 && entryDepth >= w.options.MaxDepth {
		hasEntries, err := directoryHasEntriesInternal(w.ctx, w.rootPath, entry.Path)
		if err != nil {
			return err
		}
		w.result.Truncated = w.result.Truncated || hasEntries
		return nil
	}
	*childDirectories = append(*childDirectories, walkDirectoryInternal{path: entry.Path, depth: entryDepth})
	return nil
}

func (w *boundedWalkerInternal) walkDirectoryInternal(current walkDirectoryInternal) ([]walkDirectoryInternal, error) {
	childDirectories := make([]walkDirectoryInternal, 0)
	err := ListEach(w.ctx, w.rootPath, current.path, func(entry acfstypes.Entry) error {
		return w.visitEntryInternal(current, &childDirectories, entry)
	})
	return childDirectories, err
}

// WalkBounded visits descendants using deterministic sequential traversal and
// reports whether depth or entry limits omitted any entries.
func WalkBounded(ctx context.Context, rootPath, logicalPath string, options acfstypes.WalkOptions, visit func(acfstypes.Entry) error) (acfstypes.WalkResult, error) {
	walker := boundedWalkerInternal{ctx: ctx, rootPath: rootPath, options: options, visit: visit}
	if visit == nil {
		return walker.result, errors.New("visit callback is required")
	}
	if options.MaxDepth < 0 || options.MaxEntries < 0 {
		return walker.result, errors.New("walk limits must be non-negative")
	}

	directories := []walkDirectoryInternal{{path: logicalPath}}
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return walker.result, err
		}

		last := len(directories) - 1
		currentDirectory := directories[last]
		directories = directories[:last]

		childDirectories, err := walker.walkDirectoryInternal(currentDirectory)
		if errors.Is(err, fs.SkipAll) {
			return walker.result, nil
		}
		if err != nil {
			return walker.result, err
		}

		for _, childDirectory := range slices.Backward(childDirectories) {
			directories = append(directories, childDirectory)
		}
	}

	return walker.result, nil
}

// MkdirAll creates a root-confined directory and any missing parents.
func MkdirAll(ctx context.Context, rootPath, logicalPath string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode != mode.Perm() {
		return fmt.Errorf("invalid directory mode %v", mode)
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolveForMkdirInternal(root, relativePath)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(resolvedPath, mode); err != nil {
		return fmt.Errorf("create directory %q: %w", logicalPath, err)
	}
	return nil
}

// Mkdir creates a single root-confined directory whose parent must already
// exist. An existing target reports ErrAlreadyExists, which also satisfies
// errors.Is(err, fs.ErrExist) so collision loops can match either sentinel.
func Mkdir(ctx context.Context, rootPath, logicalPath string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode != mode.Perm() {
		return fmt.Errorf("invalid directory mode %v", mode)
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return err
	}
	if relativePath == "." {
		return fmt.Errorf("%w: workspace root: %w", ErrAlreadyExists, fs.ErrExist)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParentInternal(root, relativePath)
	if err != nil {
		return err
	}
	if err := root.Mkdir(path.Join(resolvedParent, base), mode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %q: %w", ErrAlreadyExists, logicalPath, fs.ErrExist)
		}
		return fmt.Errorf("create directory %q: %w", logicalPath, err)
	}
	return nil
}

// Remove removes a root-confined file, symbolic link, or empty directory
// without recursing. A non-empty directory reports ErrNotEmpty.
//
// Unlike RemoveAll, Remove does not swallow a missing target: the underlying
// not-exist error is returned so callers such as bounded empty-parent cleanup
// loops can stop on the first failure of any kind.
func Remove(ctx context.Context, rootPath, logicalPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return err
	}
	if relativePath == "." {
		return ErrRootRemoval
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParentInternal(root, relativePath)
	if err != nil {
		return err
	}
	if err := root.Remove(path.Join(resolvedParent, base)); err != nil {
		if isDirectoryNotEmptyInternal(err) {
			return fmt.Errorf("%w: %q", ErrNotEmpty, logicalPath)
		}
		return fmt.Errorf("remove %q: %w", logicalPath, err)
	}
	return nil
}

// RemoveAll removes a root-confined file, symbolic link, or directory tree. A
// missing target is not an error.
func RemoveAll(ctx context.Context, rootPath, logicalPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return err
	}
	if relativePath == "." {
		return ErrRootRemoval
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParentInternal(root, relativePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	targetPath := path.Join(resolvedParent, base)
	if err := root.RemoveAll(targetPath); err != nil {
		return fmt.Errorf("remove %q: %w", logicalPath, err)
	}
	return nil
}
