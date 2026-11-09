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

	"kit.getarcane.app"
	kitfs "kit.getarcane.app/fs"

	acfstypes "kit.getarcane.app/acfs/types"
)

type (
	walker struct {
		ctx     context.Context
		root    Root
		visit   func(acfstypes.Entry) error
		level   walkLevel
		options acfstypes.WalkOptions
		result  acfstypes.WalkResult
	}

	walkDirectory struct {
		path  string
		depth int
	}

	walkLevel struct {
		directory walkDirectory
		children  []walkDirectory
	}
)

const (
	directoryReadBatchSize = 256
)

// ListEach visits the children of a root-confined directory in name order.
func (r Root) ListEach(ctx context.Context, logicalPath string, visit func(acfstypes.Entry) error) error {
	if visit == nil {
		return errors.New("visit callback is required")
	}
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

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()

	resolvedPath, _, err := resolvePath(root, relativePath, resolveOptions{FollowFinal: true})
	if err != nil {
		return err
	}

	names, err := readDirectory(ctx, root, resolvedPath, logicalPath)
	if err != nil {
		return err
	}

	directory, err := root.OpenRoot(resolvedPath)
	if err != nil {
		return fmt.Errorf("open directory %q: %w", logicalPath, err)
	}
	defer func() {
		_ = directory.Close()
	}()
	return visitChildren(ctx, directory, names, kit.Ternary(resolvedPath == ".", "/", "/"+resolvedPath+"/"), false, visit)
}

// List returns the direct children of a root-confined directory.
func (r Root) List(ctx context.Context, logicalPath string) ([]acfstypes.Entry, error) {
	entries := make([]acfstypes.Entry, 0)
	err := r.ListEach(ctx, logicalPath, func(entry acfstypes.Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// readDirectory returns the sorted child names of resolvedPath.
func readDirectory(ctx context.Context, root *os.Root, resolvedPath, displayPath string) ([]string, error) {
	directory, err := root.OpenFile(resolvedPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open directory %q: %w", displayPath, err)
	}
	defer func() {
		_ = directory.Close()
	}()

	var names []string
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		batch, readErr := directory.Readdirnames(directoryReadBatchSize)
		names = append(names, batch...)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read directory %q: %w", displayPath, readErr)
		}
	}
	slices.Sort(names)
	return names, nil
}

// Stat returns metadata for a root-confined path, following the final
// symbolic link only with options.FollowFinal.
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
	if info, ok := statWithoutLinks(r.Path, relativePath); ok {
		return newEntry(path.Base(relativePath), kitfs.LogicalPath(relativePath), kitfs.FormatMode(info.Mode()), "", info), nil
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()

	resolvedPath, info, err := resolvePath(root, relativePath, resolveOptions{FollowFinal: options.FollowFinal})
	if err != nil {
		return acfstypes.Entry{}, err
	}
	if info == nil {
		info, err = root.Lstat(resolvedPath)
		if err != nil {
			return acfstypes.Entry{}, fmt.Errorf("stat %q: %w", logicalPath, err)
		}
	}

	linkTarget, err := entryLinkTarget(root, resolvedPath, resolvedPath, info, false)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	return newEntry(kit.Ternary(resolvedPath == ".", "/", path.Base(resolvedPath)), kitfs.LogicalPath(resolvedPath), kitfs.FormatMode(info.Mode()), linkTarget, info), nil
}

// Exists reports whether a root-confined path exists; a dangling final link
// counts. A missing target is not an error.
func (r Root) Exists(ctx context.Context, logicalPath string) (bool, error) {
	if _, err := r.Stat(ctx, logicalPath, acfstypes.StatOptions{}); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Walk visits every descendant of a root-confined directory depth-first in
// name order without following symbolic links.
func (r Root) Walk(ctx context.Context, logicalPath string, options acfstypes.WalkOptions, visit func(acfstypes.Entry) error) (acfstypes.WalkResult, error) {
	if visit == nil {
		return acfstypes.WalkResult{}, errors.New("visit callback is required")
	}
	if options.MaxDepth < 0 || options.MaxEntries < 0 {
		return acfstypes.WalkResult{}, errors.New("walk limits must be non-negative")
	}

	w := &walker{
		ctx:     ctx,
		root:    r,
		options: options,
		visit:   visit,
	}
	visitEntry := w.entry
	directories := []walkDirectory{{path: logicalPath}}
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return w.result, err
		}

		last := len(directories) - 1
		w.level = walkLevel{
			directory: directories[last],
			children:  w.level.children[:0],
		}
		directories = directories[:last]

		err := r.ListEach(ctx, w.level.directory.path, visitEntry)
		if errors.Is(err, fs.SkipAll) {
			return w.result, nil
		}
		if err != nil {
			return w.result, err
		}

		for _, childDirectory := range slices.Backward(w.level.children) {
			directories = append(directories, childDirectory)
		}
	}

	return w.result, nil
}

func (w *walker) entry(entry acfstypes.Entry) error {
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

	depth := w.level.directory.depth + 1
	if w.options.MaxDepth > 0 && depth >= w.options.MaxDepth {
		err := w.root.ListEach(w.ctx, entry.Path, func(acfstypes.Entry) error {
			w.result.Truncated = true
			return fs.SkipAll
		})
		if err != nil && !errors.Is(err, fs.SkipAll) {
			return err
		}
		return nil
	}
	w.level.children = append(w.level.children, walkDirectory{path: entry.Path, depth: depth})
	return nil
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

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()

	if created, fastErr := mutation.top.openDir(relativePath, dirOptions{Create: true, Mode: mode}); fastErr == nil {
		return created.Close()
	}

	root, err := mutation.workspace()
	if err != nil {
		return err
	}
	components := strings.Split(relativePath, "/")
	for index := len(components); index >= 0; index-- {
		resolvedPrefix, _, resolveErr := resolvePath(root, cmp.Or(path.Join(components[:index]...), "."), resolveOptions{FollowFinal: true})
		if errors.Is(resolveErr, fs.ErrNotExist) {
			continue
		}
		if resolveErr != nil {
			return resolveErr
		}
		target := path.Join(append([]string{resolvedPrefix}, components[index:]...)...)
		created, createErr := mutation.top.openDir(target, dirOptions{Create: true, Mode: mode})
		var pathErr *fs.PathError
		if errors.As(createErr, &pathErr) && pathErr.Path == target && errors.Is(pathErr.Err, syscall.ENOTDIR) {
			createErr = &fs.PathError{Op: "mkdirat", Path: target, Err: syscall.EEXIST}
		}
		if createErr != nil {
			return fmt.Errorf("create directory %q: %w", logicalPath, createErr)
		}
		return created.Close()
	}

	return fmt.Errorf("%w: no existing workspace ancestor", ErrInvalidPath)
}

// Mkdir creates a root-confined directory whose parent exists. An existing
// target reports ErrAlreadyExists, which also matches fs.ErrExist.
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

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()

	parent, base, err := mutation.openResolvedParent(relativePath, func(target string) error {
		return fmt.Errorf("create directory %q: %w", logicalPath, &fs.PathError{Op: "mkdirat", Path: target, Err: syscall.ENOTDIR})
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	err = parent.Mkdir(base, mode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %q: %w", ErrAlreadyExists, logicalPath, fs.ErrExist)
		}
		return fmt.Errorf("create directory %q: %w", logicalPath, err)
	}
	return nil
}

// Remove removes a root-confined file, symbolic link, or empty directory. A
// non-empty directory reports ErrNotEmpty and a missing target is an error.
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

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()

	parent, base, err := mutation.openResolvedParent(relativePath, func(target string) error {
		return removeError(&fs.PathError{Op: "removeat", Path: target, Err: syscall.ENOTDIR}, logicalPath)
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	return removeError(parent.Remove(base), logicalPath)
}

// removeError wraps a removal error, reporting ErrNotEmpty for a non-empty
// directory, which some filesystems report as EEXIST.
func removeError(err error, displayPath string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("%w: %q", ErrNotEmpty, displayPath)
	}
	return fmt.Errorf("remove %q: %w", displayPath, err)
}

// RemoveAll removes a root-confined path and its children. A missing target
// is not an error.
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

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()

	parent, base, err := mutation.openResolvedParent(relativePath, func(target string) error {
		return fmt.Errorf("remove %q: %w", logicalPath, &fs.PathError{Op: "RemoveAll", Path: target, Err: syscall.ENOTDIR})
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	err = parent.RemoveAll(base)
	if err != nil {
		return fmt.Errorf("remove %q: %w", logicalPath, err)
	}
	return nil
}
