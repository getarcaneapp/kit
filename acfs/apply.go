package acfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	kitfs "go.getarcane.app/kit/pkg/fs"

	acfstypes "go.getarcane.app/acfs/types"
)

const (
	applyFileMode      = 0o644
	applyDirectoryMode = 0o755
)

// Apply performs an ordered batch of root-confined mutations. It stops at the
// first failure; callers that require transactionality remain responsible for
// restoring their own backup.
func Apply(ctx context.Context, rootPath, stagingPath string, manifest acfstypes.ApplyManifest) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if manifest.Version != acfstypes.ProtocolVersion {
		return 0, fmt.Errorf("unsupported apply manifest version %d", manifest.Version)
	}
	for index, change := range manifest.Changes {
		if err := validateApplyChange(change); err != nil {
			return 0, &acfstypes.ApplyError{
				Index:     index,
				Operation: change.Operation,
				Path:      change.Path,
				Err:       err,
			}
		}
	}
	if err := validateDistinctRoots(rootPath, stagingPath); err != nil {
		return 0, err
	}

	root, openErr := os.OpenRoot(rootPath)
	if openErr != nil {
		return 0, fmt.Errorf("open workspace root: %w", openErr)
	}
	defer func() { _ = root.Close() }()
	stagingRoot, openErr := os.OpenRoot(stagingPath)
	if openErr != nil {
		return 0, fmt.Errorf("open staging root: %w", openErr)
	}
	defer func() { _ = stagingRoot.Close() }()

	for index, change := range manifest.Changes {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		if err := func() error {
			switch change.Operation {
			case acfstypes.ApplyCreateFile:
				return applyFile(ctx, root, stagingRoot, change, true)
			case acfstypes.ApplyUpdateFile:
				return applyFile(ctx, root, stagingRoot, change, false)
			case acfstypes.ApplyCreateFolder:
				relativePath, err := normalizeMutationPath(change.Path)
				if err != nil {
					return err
				}
				parent, err := ensureMutationParent(root, relativePath, true)
				if err != nil {
					return err
				}
				parentRoot, err := root.OpenRoot(parent)
				if err != nil {
					return err
				}
				defer func() { _ = parentRoot.Close() }()
				base := path.Base(relativePath)
				err = rejectExistingTarget(parentRoot, base)
				if err != nil {
					return err
				}
				err = parentRoot.Mkdir(base, applyDirectoryMode)
				if errors.Is(err, fs.ErrExist) {
					return ErrAlreadyExists
				}
				return err
			case acfstypes.ApplyRename, acfstypes.ApplyMove:
				return applyRename(root, change.Path, change.TargetPath)
			case acfstypes.ApplyDelete:
				return applyDelete(root, change.Path, change.Recursive)
			}
			return nil
		}(); err != nil {
			return index, &acfstypes.ApplyError{
				Index:     index,
				Operation: change.Operation,
				Path:      change.Path,
				Err:       err,
			}
		}
	}
	return len(manifest.Changes), nil
}

func normalizeMutationPath(logicalPath string) (string, error) {
	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return "", err
	}
	if relativePath == "." {
		return "", fmt.Errorf("%w: workspace root is not a mutation target", ErrInvalidPath)
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return "", err
	}
	return relativePath, nil
}

func ensureMutationParent(root *os.Root, relativePath string, create bool) (string, error) {
	parent := path.Dir(relativePath)
	if parent == "." {
		return ".", nil
	}

	current := ""
	for component := range strings.SplitSeq(parent, "/") {
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) && create {
			err = root.Mkdir(current, applyDirectoryMode)
			if err != nil && !errors.Is(err, fs.ErrExist) {
				return "", fmt.Errorf("create parent %q: %w", kitfs.LogicalPath(current), err)
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %s", ErrSymlink, kitfs.LogicalPath(current))
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%w: %s", ErrNotDirectory, kitfs.LogicalPath(current))
		}
	}
	return parent, nil
}

// rejectExistingTarget reports ErrAlreadyExists, or ErrSymlink for a
// symbolic link, when name already exists in root.
func rejectExistingTarget(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlink
		}
		return ErrAlreadyExists
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func applyFile(ctx context.Context, root, stagingRoot *os.Root, change acfstypes.ApplyChange, create bool) error {
	relativePath, err := normalizeMutationPath(change.Path)
	if err != nil {
		return err
	}
	parent, err := ensureMutationParent(root, relativePath, create)
	if err != nil {
		return err
	}
	parentRoot, err := root.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer func() { _ = parentRoot.Close() }()
	base := path.Base(relativePath)

	// For updates, mode is the permission bits of an existing regular file in
	// root, refusing symbolic links and other file types.
	mode, err := func() (os.FileMode, error) {
		if create {
			return applyFileMode, rejectExistingTarget(parentRoot, base)
		}
		info, lstatErr := parentRoot.Lstat(base)
		if lstatErr != nil {
			return 0, lstatErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return 0, ErrSymlink
		}
		if !info.Mode().IsRegular() {
			return 0, ErrNotFile
		}
		return info.Mode() & chmodModeMask, nil
	}()
	if err != nil {
		return err
	}

	stagedInfo, err := stagingRoot.Lstat(change.StagedName)
	if err != nil {
		return err
	}
	if !stagedInfo.Mode().IsRegular() {
		return ErrNotFile
	}
	if stagedInfo.Size() != change.Size {
		return fmt.Errorf("%w: staged file has %d bytes, expected %d", ErrSizeMismatch, stagedInfo.Size(), change.Size)
	}
	staged, err := stagingRoot.Open(change.StagedName)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close() }()

	if !create {
		_, err = writeFromRoot(ctx, parentRoot, change.Path, base, staged, change.Size, mode)
		return err
	}

	target, temporaryPath, err := createTemporaryFile(parentRoot, ".")
	if err != nil {
		return err
	}
	defer func() {
		_ = target.Close()
		_ = parentRoot.Remove(temporaryPath)
	}()

	written, err := io.CopyN(target, contextReader{ctx: ctx, reader: staged}, change.Size)
	if err != nil {
		return fmt.Errorf("%w: expected %d bytes, received %d: %w", ErrSizeMismatch, change.Size, written, err)
	}
	err = target.Chmod(applyFileMode)
	if err != nil {
		return err
	}
	err = target.Sync()
	if err != nil {
		return err
	}
	err = target.Close()
	if err != nil {
		return err
	}
	err = parentRoot.Link(temporaryPath, base)
	if errors.Is(err, fs.ErrExist) {
		return ErrAlreadyExists
	}
	if err != nil {
		return err
	}
	return parentRoot.Remove(temporaryPath)
}

func applyRename(root *os.Root, sourcePath, targetPath string) error {
	source, err := normalizeMutationPath(sourcePath)
	if err != nil {
		return err
	}
	target, err := normalizeMutationPath(targetPath)
	if err != nil {
		return err
	}
	if source == target || strings.HasPrefix(target, source+"/") {
		return fmt.Errorf("%w: invalid destination", ErrInvalidPath)
	}
	_, err = ensureMutationParent(root, source, false)
	if err != nil {
		return err
	}
	info, err := root.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrSymlink, kitfs.LogicalPath(source))
	}
	_, err = ensureMutationParent(root, target, false)
	if err != nil {
		return err
	}
	err = rejectExistingTarget(root, target)
	if err != nil {
		return err
	}
	return root.Rename(source, target)
}

func applyDelete(root *os.Root, logicalPath string, recursive bool) error {
	if logicalPath == "/" {
		return ErrRootRemoval
	}
	relativePath, err := normalizeMutationPath(logicalPath)
	if err != nil {
		return err
	}
	parent, err := ensureMutationParent(root, relativePath, false)
	if err != nil {
		return err
	}
	parentRoot, err := root.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer func() { _ = parentRoot.Close() }()
	base := path.Base(relativePath)
	info, err := parentRoot.Lstat(base)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	if info.IsDir() && recursive {
		return parentRoot.RemoveAll(base)
	}
	err = parentRoot.Remove(base)
	if err != nil {
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return ErrNotEmpty
		}
		return err
	}
	return nil
}

func validateApplyChange(change acfstypes.ApplyChange) error {
	if change.Path == "/" && change.Operation == acfstypes.ApplyDelete {
		return ErrRootRemoval
	}
	if _, err := normalizeMutationPath(change.Path); err != nil {
		return err
	}
	switch change.Operation {
	case acfstypes.ApplyCreateFile, acfstypes.ApplyUpdateFile:
		if change.TargetPath != "" || change.Recursive || change.Size < 0 {
			return fmt.Errorf("%w: operation has unexpected fields", ErrInvalidPath)
		}
		if change.StagedName == "" || change.StagedName == "." || change.StagedName == ".." || path.Base(change.StagedName) != change.StagedName || strings.ContainsAny(change.StagedName, "/\\\x00") {
			return fmt.Errorf("%w: invalid staged name %q", ErrInvalidPath, change.StagedName)
		}
		return nil
	case acfstypes.ApplyCreateFolder:
		if change.TargetPath != "" || change.StagedName != "" || change.Size != 0 || change.Recursive {
			return fmt.Errorf("%w: operation has unexpected fields", ErrInvalidPath)
		}
		return nil
	case acfstypes.ApplyDelete:
		if change.TargetPath != "" || change.StagedName != "" || change.Size != 0 {
			return fmt.Errorf("%w: operation has unexpected fields", ErrInvalidPath)
		}
		return nil
	case acfstypes.ApplyRename, acfstypes.ApplyMove:
		if change.StagedName != "" || change.Size != 0 || change.Recursive {
			return fmt.Errorf("%w: operation has unexpected fields", ErrInvalidPath)
		}
		if _, err := normalizeMutationPath(change.TargetPath); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported operation %q", ErrInvalidPath, change.Operation)
	}
}

func validateDistinctRoots(rootPath, stagingPath string) error {
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	staging, err := filepath.EvalSymlinks(stagingPath)
	if err != nil {
		return fmt.Errorf("resolve staging root: %w", err)
	}
	for _, paths := range [][2]string{{root, staging}, {staging, root}} {
		relative, relErr := filepath.Rel(paths[0], paths[1])
		if relErr != nil {
			return relErr
		}
		if filepath.IsLocal(relative) {
			return fmt.Errorf("%w: workspace and staging roots overlap", ErrInvalidPath)
		}
	}
	return nil
}
