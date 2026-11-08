package acfs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"syscall"

	kitfs "kit.getarcane.app/fs"

	acfstypes "kit.getarcane.app/acfs/types"
)

const directoryReadBatchSize = 256

// ListEach visits the direct children of a root-confined directory in
// deterministic name order without retaining all metadata in memory.
func (r Root) ListEach(ctx context.Context, logicalPath string, visit func(acfstypes.Entry) error) error {
	_, err := r.ListEachBounded(ctx, logicalPath, acfstypes.ListOptions{}, visit)
	return err
}

// ListEachBounded is ListEach with options.Limit capping the number of visited
// entries. Every name is still read and sorted to keep the order
// deterministic, but only the visited entries and one more, to report
// truncation, are inspected.
func (r Root) ListEachBounded(ctx context.Context, logicalPath string, options acfstypes.ListOptions, visit func(acfstypes.Entry) error) (acfstypes.ListResult, error) {
	var result acfstypes.ListResult
	if visit == nil {
		return result, errors.New("visit callback is required")
	}
	if options.Limit < 0 {
		return result, errors.New("list limit must be non-negative")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return result, err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return result, err
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return result, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePath(root, relativePath, true)
	if err != nil {
		return result, err
	}

	names, err := func() ([]string, error) {
		directory, openErr := root.Open(resolvedPath)
		if openErr != nil {
			return nil, fmt.Errorf("open directory %q: %w", logicalPath, openErr)
		}
		defer func() { _ = directory.Close() }()

		names := make([]string, 0, directoryReadBatchSize)
		for {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
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
	}()
	if err != nil {
		return result, err
	}

	for _, name := range names {
		if strings.HasPrefix(name, temporaryWritePrefix) {
			continue
		}
		err = ctx.Err()
		if err != nil {
			return result, err
		}

		entryPath := path.Join(resolvedPath, name)
		info, statErr := root.Lstat(entryPath)
		// Skip a child that disappeared after its parent directory was read.
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if options.Limit > 0 && result.Count >= options.Limit {
			// The child still exists, so the limit omitted at least one entry.
			result.Truncated = true
			return result, nil
		}
		if statErr != nil {
			return result, fmt.Errorf("stat %q: %w", kitfs.LogicalPath(entryPath), statErr)
		}

		entry, entryErr := entryFromInfo(root, entryPath, info)
		if errors.Is(entryErr, fs.ErrNotExist) {
			continue
		}
		if entryErr != nil {
			return result, entryErr
		}
		err = visit(entry)
		if err != nil {
			return result, fmt.Errorf("visit %q: %w", entry.Path, err)
		}
		result.Count++
	}
	return result, nil
}

// List returns the direct children of a root-confined directory.
func (r Root) List(ctx context.Context, logicalPath string) ([]acfstypes.Entry, error) {
	entries, _, err := r.ListBounded(ctx, logicalPath, acfstypes.ListOptions{})
	return entries, err
}

// ListBounded is List with options.Limit capping the number of returned
// entries. The result reports whether the limit omitted any entries.
func (r Root) ListBounded(ctx context.Context, logicalPath string, options acfstypes.ListOptions) ([]acfstypes.Entry, acfstypes.ListResult, error) {
	entries := make([]acfstypes.Entry, 0)
	result, err := r.ListEachBounded(ctx, logicalPath, options, func(entry acfstypes.Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, acfstypes.ListResult{}, err
	}
	return entries, result, nil
}

// Stat returns metadata for a root-confined path. By default the final symbolic
// link is not followed, so the Entry describes the link itself; set
// options.FollowFinal to resolve it.
func (r Root) Stat(ctx context.Context, logicalPath string, options acfstypes.StatOptions) (acfstypes.Entry, error) {
	if err := ctx.Err(); err != nil {
		return acfstypes.Entry{}, err
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return acfstypes.Entry{}, err
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePath(root, relativePath, options.FollowFinal)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	info, err := root.Lstat(resolvedPath)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("stat %q: %w", logicalPath, err)
	}

	return entryFromInfo(root, resolvedPath, info)
}

// Exists reports whether a root-confined path exists without following its
// final symbolic link, so a dangling link still counts as existing. A target
// missing anywhere along the path is reported as (false, nil); every other
// failure — including a path that escapes the root — is returned as an error.
func (r Root) Exists(ctx context.Context, logicalPath string) (bool, error) {
	if _, err := r.Stat(ctx, logicalPath, acfstypes.StatOptions{}); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Walk visits every descendant of a root-confined directory in deterministic
// depth-first order and reports whether options' depth or entry limits omitted
// any entries. It does not follow symbolic links encountered in the tree.
func (r Root) Walk(ctx context.Context, logicalPath string, options acfstypes.WalkOptions, visit func(acfstypes.Entry) error) (acfstypes.WalkResult, error) {
	if visit == nil {
		return acfstypes.WalkResult{}, errors.New("visit callback is required")
	}
	if options.MaxDepth < 0 || options.MaxEntries < 0 {
		return acfstypes.WalkResult{}, errors.New("walk limits must be non-negative")
	}

	count := 0
	truncated := false
	directories := []walkDirectory{{path: logicalPath}}
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return acfstypes.WalkResult{Count: count, Truncated: truncated}, err
		}

		last := len(directories) - 1
		currentDirectory := directories[last]
		directories = directories[:last]

		childDirectories := make([]walkDirectory, 0)
		err := r.ListEach(ctx, currentDirectory.path, func(entry acfstypes.Entry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if options.MaxEntries > 0 && count >= options.MaxEntries {
				truncated = true
				return fs.SkipAll
			}

			visitErr := visit(entry)
			if visitErr != nil && !errors.Is(visitErr, fs.SkipDir) && !errors.Is(visitErr, fs.SkipAll) {
				return visitErr
			}
			count++
			if errors.Is(visitErr, fs.SkipAll) {
				return fs.SkipAll
			}
			if !entry.IsDirectory || entry.IsSymlink || errors.Is(visitErr, fs.SkipDir) {
				return nil
			}

			if options.MaxDepth > 0 && currentDirectory.depth+1 >= options.MaxDepth {
				err := r.ListEach(ctx, entry.Path, func(acfstypes.Entry) error {
					truncated = true
					return fs.SkipAll
				})
				if err != nil && !errors.Is(err, fs.SkipAll) {
					return err
				}
				return nil
			}
			childDirectories = append(childDirectories, walkDirectory{path: entry.Path, depth: currentDirectory.depth + 1})
			return nil
		})
		if errors.Is(err, fs.SkipAll) {
			return acfstypes.WalkResult{Count: count, Truncated: truncated}, nil
		}
		if err != nil {
			return acfstypes.WalkResult{Count: count, Truncated: truncated}, err
		}

		for _, childDirectory := range slices.Backward(childDirectories) {
			directories = append(directories, childDirectory)
		}
	}

	return acfstypes.WalkResult{Count: count, Truncated: truncated}, nil
}

// MkdirAll creates a root-confined directory and any missing parents.
func (r Root) MkdirAll(ctx context.Context, logicalPath string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode != mode.Perm() {
		return fmt.Errorf("invalid directory mode %v", mode)
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	components := strings.Split(relativePath, "/")
	for index := len(components); index >= 0; index-- {
		resolvedPrefix, resolveErr := resolvePath(root, cmp.Or(path.Join(components[:index]...), "."), true)
		if errors.Is(resolveErr, fs.ErrNotExist) {
			continue
		}
		if resolveErr != nil {
			return resolveErr
		}
		err = root.MkdirAll(path.Join(append([]string{resolvedPrefix}, components[index:]...)...), mode)
		if err != nil {
			return fmt.Errorf("create directory %q: %w", logicalPath, err)
		}
		return nil
	}

	return fmt.Errorf("%w: no existing workspace ancestor", ErrInvalidPath)
}

// Mkdir creates a single root-confined directory whose parent must already
// exist. An existing target reports ErrAlreadyExists, which also satisfies
// errors.Is(err, fs.ErrExist) so collision loops can match either sentinel.
func (r Root) Mkdir(ctx context.Context, logicalPath string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode != mode.Perm() {
		return fmt.Errorf("invalid directory mode %v", mode)
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return fmt.Errorf("%w: workspace root: %w", ErrAlreadyExists, fs.ErrExist)
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParent(root, relativePath)
	if err != nil {
		return err
	}
	err = root.Mkdir(path.Join(resolvedParent, base), mode)
	if err != nil {
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
func (r Root) Remove(ctx context.Context, logicalPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return ErrRootRemoval
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParent(root, relativePath)
	if err != nil {
		return err
	}
	err = root.Remove(path.Join(resolvedParent, base))
	if err != nil {
		// Report whether a removal or rename failed because the destination is a
		// non-empty directory. Linux reports ENOTEMPTY, while some filesystems and
		// platforms report EEXIST for the same condition.
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("%w: %q", ErrNotEmpty, logicalPath)
		}
		return fmt.Errorf("remove %q: %w", logicalPath, err)
	}
	return nil
}

// RemoveAll removes a root-confined file, symbolic link, or directory tree. A
// missing target is not an error.
func (r Root) RemoveAll(ctx context.Context, logicalPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return ErrRootRemoval
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParent(root, relativePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	err = root.RemoveAll(path.Join(resolvedParent, base))
	if err != nil {
		return fmt.Errorf("remove %q: %w", logicalPath, err)
	}
	return nil
}

type walkDirectory struct {
	path  string
	depth int
}
