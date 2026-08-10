package acfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"syscall"

	"go.getarcane.app/acfs/pkg/utils"
)

// isDirectoryNotEmptyInternal reports whether a removal or rename failed
// because the destination is a non-empty directory. Linux reports ENOTEMPTY,
// while some filesystems and platforms report EEXIST for the same condition.
func isDirectoryNotEmptyInternal(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}

func resolveRenameEndpointInternal(root *os.Root, logicalPath string) (string, error) {
	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return "", err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return "", err
	}
	if relativePath == "." {
		return "", ErrRootRemoval
	}

	resolvedParent, base, err := resolveParentInternal(root, relativePath)
	if err != nil {
		return "", err
	}
	return path.Join(resolvedParent, base), nil
}

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

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	sourcePath, err := resolveRenameEndpointInternal(root, sourceLogical)
	if err != nil {
		return err
	}
	targetPath, err := resolveRenameEndpointInternal(root, targetLogical)
	if err != nil {
		if errors.Is(err, ErrRootRemoval) {
			return fmt.Errorf("%w: cannot rename onto the workspace root", ErrInvalidPath)
		}
		return err
	}

	if strings.HasPrefix(targetPath, sourcePath+"/") {
		return fmt.Errorf("%w: target %q is inside source %q", ErrInvalidPath, targetLogical, sourceLogical)
	}

	if err := root.Rename(sourcePath, targetPath); err != nil {
		if isDirectoryNotEmptyInternal(err) {
			return fmt.Errorf("%w: %q", ErrNotEmpty, targetLogical)
		}
		return fmt.Errorf("rename %q to %q: %w", sourceLogical, targetLogical, err)
	}
	return nil
}
