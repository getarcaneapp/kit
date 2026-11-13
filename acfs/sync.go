package acfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"syscall"

	kitfs "kit.getarcane.app/fs"
)

// Sync flushes a root-confined regular file or directory to stable storage.
// Links in the parent path are resolved; a final link is refused with ErrSymlink.
func (r Root) Sync(ctx context.Context, logicalPath string) error {
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

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()
	if relativePath == "." {
		return syncIn(mutation.top, ".", logicalPath)
	}
	parent, base, err := mutation.openResolvedParent(relativePath, func(target string) error {
		return fmt.Errorf("sync %q: %w", logicalPath, &fs.PathError{
			Op:   "openat",
			Path: path.Dir(target),
			Err:  syscall.ENOTDIR,
		})
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	return syncIn(parent, base, logicalPath)
}

// Sync flushes a host regular file or directory to stable storage, following
// symbolic links.
func (Host) Sync(ctx context.Context, hostPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(targetPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return openError(err, targetPath)
	}
	return syncAndClose(file, targetPath)
}

// syncIn opens name in files read-only without following it and syncs it.
func syncIn(files *dir, name, displayPath string) error {
	file, err := files.OpenFile(name, os.O_RDONLY, 0)
	if errors.Is(err, ErrSymlink) || errors.Is(err, ErrNotFile) {
		return err
	}
	if err != nil {
		return fmt.Errorf("open %q: %w", displayPath, err)
	}
	return syncAndClose(file, displayPath)
}

// syncAndClose syncs a regular file or directory and closes it.
func syncAndClose(file *os.File, displayPath string) (retErr error) {
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", displayPath, err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q", ErrNotFile, displayPath)
	}
	err = file.Sync()
	if err != nil {
		return fmt.Errorf("sync %q: %w", displayPath, err)
	}
	return nil
}
