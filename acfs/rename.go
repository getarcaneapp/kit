package acfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"syscall"

	kitfs "go.getarcane.app/kit/pkg/fs"
)

// Rename moves sourceLogical onto targetLogical within a single workspace
// root, with plain os.Rename semantics: an existing file or empty directory at
// the target is replaced, and a non-empty directory target reports ErrNotEmpty.
//
// Both parent directories are resolved through symbolic links; neither final
// component is followed, so renaming a symlink moves the link itself. Renaming
// the workspace root reports ErrRootRemoval and a target nested inside the
// source reports ErrInvalidPath.
//
// A cross-device rename (EXDEV) is returned unchanged. Staying on one device is
// the caller's contract — staging directories created with MkdirTemp under the
// same root always satisfy it.
func Rename(ctx context.Context, rootPath, sourceLogical, targetLogical string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	resolveEndpoint := func(root *os.Root, logicalPath string) (string, error) {
		relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
		if err != nil {
			return "", err
		}
		err = rejectReservedPath(relativePath)
		if err != nil {
			return "", err
		}
		if relativePath == "." {
			return "", ErrRootRemoval
		}

		resolvedParent, base, err := resolveParent(root, relativePath)
		if err != nil {
			return "", err
		}
		return path.Join(resolvedParent, base), nil
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	sourcePath, err := resolveEndpoint(root, sourceLogical)
	if err != nil {
		return err
	}
	targetPath, err := resolveEndpoint(root, targetLogical)
	if err != nil {
		if errors.Is(err, ErrRootRemoval) {
			return fmt.Errorf("%w: cannot rename onto the workspace root", ErrInvalidPath)
		}
		return err
	}

	if strings.HasPrefix(targetPath, sourcePath+"/") {
		return fmt.Errorf("%w: target %q is inside source %q", ErrInvalidPath, targetLogical, sourceLogical)
	}

	err = root.Rename(sourcePath, targetPath)
	if err != nil {
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("%w: %q", ErrNotEmpty, targetLogical)
		}
		return fmt.Errorf("rename %q to %q: %w", sourceLogical, targetLogical, err)
	}
	return nil
}
