package acfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"kit.getarcane.app"
	kitfs "kit.getarcane.app/fs"

	acfstypes "kit.getarcane.app/acfs/types"
)

// ListEach visits the children of a host directory in name order, bounded by
// options like Root.ListEach.
func (Host) ListEach(ctx context.Context, hostPath string, options acfstypes.ListOptions, visit func(acfstypes.Entry) error) (acfstypes.ListResult, error) {
	if visit == nil {
		return acfstypes.ListResult{}, errors.New("visit callback is required")
	}
	if options.MaxEntries < 0 {
		return acfstypes.ListResult{}, errors.New("list limit must be non-negative")
	}
	if err := ctx.Err(); err != nil {
		return acfstypes.ListResult{}, err
	}
	directoryPath, err := cleanHostPath(hostPath)
	if err != nil {
		return acfstypes.ListResult{}, err
	}

	// os.Root only stats children relative to the descriptor; nothing is confined.
	directory, err := os.OpenRoot(directoryPath)
	if err != nil {
		return acfstypes.ListResult{}, fmt.Errorf("open directory %q: %w", directoryPath, err)
	}
	defer func() {
		_ = directory.Close()
	}()
	names, truncated, err := readDirectory(ctx, directory, ".", directoryPath, options.MaxEntries)
	if err != nil {
		return acfstypes.ListResult{}, err
	}

	count, err := visitChildren(ctx, directory, names, kit.Ternary(filepath.Dir(directoryPath) == directoryPath, directoryPath, directoryPath+string(filepath.Separator)), true, visit)
	return acfstypes.ListResult{Count: count, Truncated: truncated}, err
}

// List returns the direct children of a host directory.
func (h Host) List(ctx context.Context, hostPath string) ([]acfstypes.Entry, error) {
	entries := make([]acfstypes.Entry, 0)
	_, err := h.ListEach(ctx, hostPath, acfstypes.ListOptions{}, func(entry acfstypes.Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// hostLinkTarget returns the stored target of a symbolic link, unmodified.
func hostLinkTarget(entryPath string, info os.FileInfo) (string, error) {
	if info.Mode()&os.ModeSymlink == 0 {
		return "", nil
	}
	target, err := os.Readlink(entryPath)
	if err != nil {
		return "", fmt.Errorf("readlink %q: %w", entryPath, err)
	}
	return target, nil
}

// Stat returns metadata for a host path, following symbolic links only with
// options.FollowFinal.
func (Host) Stat(ctx context.Context, hostPath string, options acfstypes.StatOptions) (acfstypes.Entry, error) {
	if err := ctx.Err(); err != nil {
		return acfstypes.Entry{}, err
	}
	entryPath, err := cleanHostPath(hostPath)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	if options.FollowFinal {
		entryPath, err = filepath.EvalSymlinks(entryPath)
		if err != nil {
			return acfstypes.Entry{}, fmt.Errorf("stat %q: %w", hostPath, err)
		}
	}
	info, err := os.Lstat(entryPath)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("stat %q: %w", entryPath, err)
	}
	linkTarget, err := hostLinkTarget(entryPath, info)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	return newEntry(filepath.Base(entryPath), entryPath, kitfs.FormatMode(info.Mode()), linkTarget, info), nil
}

// Exists reports whether a host path exists. A missing target is not an error.
func (h Host) Exists(ctx context.Context, hostPath string) (bool, error) {
	if _, err := h.Stat(ctx, hostPath, acfstypes.StatOptions{}); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MkdirAll creates a host directory and any missing parents.
func (Host) MkdirAll(ctx context.Context, hostPath string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode != mode.Perm() {
		return fmt.Errorf("invalid directory mode %v", mode)
	}
	directoryPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	err = os.MkdirAll(directoryPath, mode)
	if err != nil {
		return fmt.Errorf("create directory %q: %w", directoryPath, err)
	}
	return nil
}

// Mkdir creates a host directory. An existing target reports ErrAlreadyExists.
func (Host) Mkdir(ctx context.Context, hostPath string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode != mode.Perm() {
		return fmt.Errorf("invalid directory mode %v", mode)
	}
	directoryPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	err = os.Mkdir(directoryPath, mode)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %q: %w", ErrAlreadyExists, directoryPath, fs.ErrExist)
	}
	if err != nil {
		return fmt.Errorf("create directory %q: %w", directoryPath, err)
	}
	return nil
}

// Remove removes a host file, symbolic link, or empty directory like Root.Remove.
func (Host) Remove(ctx context.Context, hostPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	// A filesystem root, including a drive or UNC share root, is its own parent.
	if filepath.Dir(targetPath) == targetPath {
		return ErrRootRemoval
	}
	return removeError(os.Remove(targetPath), targetPath)
}

// RemoveAll removes a host path and its children. A missing target is not an error.
func (Host) RemoveAll(ctx context.Context, hostPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	if filepath.Dir(targetPath) == targetPath {
		return ErrRootRemoval
	}
	err = os.RemoveAll(targetPath)
	if err != nil {
		return fmt.Errorf("remove %q: %w", targetPath, err)
	}
	return nil
}
