package acfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
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
	relativePath, err := normalizeRootPath(logicalPath)
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
func (h Host) Chmod(ctx context.Context, hostPath string, mode os.FileMode) error {
	if mode&^chmodModeMask != 0 {
		return fmt.Errorf("invalid file mode %v", mode)
	}
	return h.changeAttributes(ctx, "chmod", hostPath, func(targetPath string) error {
		return os.Chmod(targetPath, mode)
	})
}

// Chown changes the owner of a host path, following symbolic links.
func (h Host) Chown(ctx context.Context, hostPath string, uid, gid int) error {
	return h.changeAttributes(ctx, "chown", hostPath, func(targetPath string) error {
		return os.Chown(targetPath, uid, gid)
	})
}

// Lchown changes the owner of a host path without following its final link.
func (h Host) Lchown(ctx context.Context, hostPath string, uid, gid int) error {
	return h.changeAttributes(ctx, "lchown", hostPath, func(targetPath string) error {
		return os.Lchown(targetPath, uid, gid)
	})
}

// Chtimes changes the times of a host path, following symbolic links.
func (h Host) Chtimes(ctx context.Context, hostPath string, atime, mtime time.Time) error {
	return h.changeAttributes(ctx, "chtimes", hostPath, func(targetPath string) error {
		return os.Chtimes(targetPath, atime, mtime)
	})
}

func (Host) changeAttributes(ctx context.Context, op, hostPath string, change func(targetPath string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath, err := cleanHostPath(hostPath)
	if err != nil {
		return err
	}
	if err = change(targetPath); err != nil {
		return fmt.Errorf("%s %q: %w", op, targetPath, err)
	}
	return nil
}
