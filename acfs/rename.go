package acfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"syscall"

	kitfs "kit.getarcane.app/fs"
)

// Rename moves sourceLogical onto targetLogical inside the root like os.Rename,
// without following either final component. A cross-device rename reports EXDEV.
func (r Root) Rename(ctx context.Context, sourceLogical, targetLogical string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()

	root, err := mutation.workspace()
	if err != nil {
		return err
	}
	sourceDir, sourceBase, err := resolveRenameEndpoint(root, sourceLogical)
	if err != nil {
		return err
	}
	targetDir, targetBase, err := resolveRenameEndpoint(root, targetLogical)
	if errors.Is(err, ErrRootRemoval) {
		return fmt.Errorf("%w: cannot rename onto the workspace root", ErrInvalidPath)
	}
	if err != nil {
		return err
	}
	sourcePath := path.Join(sourceDir, sourceBase)
	targetPath := path.Join(targetDir, targetBase)
	if strings.HasPrefix(targetPath, sourcePath+"/") {
		return fmt.Errorf("%w: target %q is inside source %q", ErrInvalidPath, targetLogical, sourceLogical)
	}

	openEndpointParent := func(relativePath string) (*dir, error) {
		parent, openErr := mutation.top.openDir(relativePath, dirOptions{})
		if errors.Is(openErr, syscall.ENOTDIR) {
			openErr = &os.LinkError{Op: "renameat", Old: sourcePath, New: targetPath, Err: syscall.ENOTDIR}
			return nil, renameError(openErr, sourceLogical, targetLogical)
		}
		return parent, openErr
	}
	sourceParent, err := openEndpointParent(sourceDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = sourceParent.Close()
	}()
	targetParent := sourceParent
	if targetDir != sourceDir {
		targetParent, err = openEndpointParent(targetDir)
		if err != nil {
			return err
		}
		defer func() {
			_ = targetParent.Close()
		}()
	}

	return renameError(sourceParent.renameTo(sourceBase, targetParent, targetBase), sourceLogical, targetLogical)
}

// renameError wraps a rename error, reporting ErrNotEmpty for a non-empty target.
func renameError(err error, source, target string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("%w: %q", ErrNotEmpty, target)
	}
	return fmt.Errorf("rename %q to %q: %w", source, target, err)
}

// resolveRenameEndpoint resolves the parent and base name of a rename endpoint.
func resolveRenameEndpoint(root *os.Root, logicalPath string) (string, string, error) {
	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return "", "", err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return "", "", err
	}
	if relativePath == "." {
		return "", "", ErrRootRemoval
	}
	return resolveParent(root, relativePath)
}
