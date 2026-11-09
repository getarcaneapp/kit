package acfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	kitfs "kit.getarcane.app/fs"
)

// Chmod changes the mode of a root-confined path, refusing symbolic links with
// ErrSymlink.
func (r Root) Chmod(ctx context.Context, logicalPath string, mode os.FileMode) error {
	if mode&^chmodModeMask != 0 {
		return fmt.Errorf("invalid file mode %v", mode)
	}
	return r.changeAttributes(ctx, "chmod", logicalPath, func(parent *dir, name string) error {
		return parent.Chmod(name, mode)
	})
}

// Chown changes the owner of a root-confined path like Chmod. A uid or gid of -1
// is unchanged.
func (r Root) Chown(ctx context.Context, logicalPath string, uid, gid int) error {
	return r.changeAttributes(ctx, "chown", logicalPath, func(parent *dir, name string) error {
		return parent.Chown(name, uid, gid)
	})
}

// Lchown changes the owner of a root-confined path without following its final link.
func (r Root) Lchown(ctx context.Context, logicalPath string, uid, gid int) error {
	return r.changeAttributes(ctx, "lchown", logicalPath, func(parent *dir, name string) error {
		return parent.Lchown(name, uid, gid)
	})
}

// Chtimes changes the times of a root-confined path like Chmod. A zero time is
// unchanged.
func (r Root) Chtimes(ctx context.Context, logicalPath string, atime, mtime time.Time) error {
	return r.changeAttributes(ctx, "chtimes", logicalPath, func(parent *dir, name string) error {
		return parent.Chtimes(name, atime, mtime)
	})
}

// changeAttributes applies change to the final component inside its parent.
func (r Root) changeAttributes(ctx context.Context, op, logicalPath string, change func(parent *dir, name string) error) error {
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
	parent, base, err := mutation.openParent(relativePath, false)
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()

	err = change(parent, base)
	if errors.Is(err, ErrSymlink) {
		return err
	}
	if err != nil {
		return fmt.Errorf("%s %q: %w", op, logicalPath, err)
	}
	return nil
}

// Chmod changes the mode of a host path, following symbolic links.
func (Host) Chmod(ctx context.Context, hostPath string, mode os.FileMode) error {
	if mode&^chmodModeMask != 0 {
		return fmt.Errorf("invalid file mode %v", mode)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	err = os.Chmod(targetPath, mode)
	if err != nil {
		return fmt.Errorf("chmod %q: %w", targetPath, err)
	}
	return nil
}

// Chown changes the owner of a host path, following symbolic links.
func (Host) Chown(ctx context.Context, hostPath string, uid, gid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	err = os.Chown(targetPath, uid, gid)
	if err != nil {
		return fmt.Errorf("chown %q: %w", targetPath, err)
	}
	return nil
}

// Lchown changes the owner of a host path without following its final link.
func (Host) Lchown(ctx context.Context, hostPath string, uid, gid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	err = os.Lchown(targetPath, uid, gid)
	if err != nil {
		return fmt.Errorf("lchown %q: %w", targetPath, err)
	}
	return nil
}

// Chtimes changes the times of a host path, following symbolic links.
func (Host) Chtimes(ctx context.Context, hostPath string, atime, mtime time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	err = os.Chtimes(targetPath, atime, mtime)
	if err != nil {
		return fmt.Errorf("chtimes %q: %w", targetPath, err)
	}
	return nil
}
