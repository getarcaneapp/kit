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

	"go.getarcane.app/acfs/pkg/utils"
	acfstypes "go.getarcane.app/acfs/types"
)

const (
	applyFileMode      = 0o644
	applyDirectoryMode = 0o755
)

func normalizeMutationPathInternal(logicalPath string) (string, error) {
	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return "", err
	}
	if relativePath == "." {
		return "", fmt.Errorf("%w: workspace root is not a mutation target", ErrInvalidPath)
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
		return "", err
	}
	return relativePath, nil
}

func inspectMutationPathInternal(root *os.Root, relativePath string) (os.FileInfo, error) {
	current := ""
	components := strings.Split(relativePath, "/")
	for index, component := range components {
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s", ErrSymlink, utils.LogicalPath(current))
		}
		if index < len(components)-1 && !info.IsDir() {
			return nil, fmt.Errorf("%w: %s", ErrNotDirectory, utils.LogicalPath(current))
		}
		if index == len(components)-1 {
			return info, nil
		}
	}
	return nil, fs.ErrNotExist
}

func ensureMutationParentInternal(root *os.Root, relativePath string, create bool) (string, error) {
	parent := path.Dir(relativePath)
	if parent == "." {
		return ".", nil
	}

	current := ""
	for component := range strings.SplitSeq(parent, "/") {
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) && create {
			if err := root.Mkdir(current, applyDirectoryMode); err != nil && !errors.Is(err, fs.ErrExist) {
				return "", fmt.Errorf("create parent %q: %w", utils.LogicalPath(current), err)
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %s", ErrSymlink, utils.LogicalPath(current))
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%w: %s", ErrNotDirectory, utils.LogicalPath(current))
		}
	}
	return parent, nil
}

func validateStagedNameInternal(name string) error {
	if name == "" || name == "." || name == ".." || path.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%w: invalid staged name %q", ErrInvalidPath, name)
	}
	return nil
}

func openStagedFileInternal(stagingRoot *os.Root, change acfstypes.ApplyChange) (*os.File, error) {
	if err := validateStagedNameInternal(change.StagedName); err != nil {
		return nil, err
	}
	if change.Size < 0 {
		return nil, fmt.Errorf("%w: size must be non-negative", ErrSizeMismatch)
	}
	info, err := stagingRoot.Lstat(change.StagedName)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotFile
	}
	if info.Size() != change.Size {
		return nil, fmt.Errorf("%w: staged file has %d bytes, expected %d", ErrSizeMismatch, info.Size(), change.Size)
	}
	return stagingRoot.Open(change.StagedName)
}

func copyCreateFileInternal(ctx context.Context, root *os.Root, targetPath string, source io.Reader, size int64) error {
	target, temporaryPath, err := createTemporaryFileInternal(root, ".")
	if err != nil {
		return err
	}
	defer func() {
		_ = target.Close()
		_ = root.Remove(temporaryPath)
	}()

	written, err := io.CopyN(target, contextReaderInternal{ctx: ctx, reader: source}, size)
	if err != nil {
		return fmt.Errorf("%w: expected %d bytes, received %d: %w", ErrSizeMismatch, size, written, err)
	}
	if err := target.Chmod(applyFileMode); err != nil {
		return err
	}
	if err := target.Sync(); err != nil {
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	if err := root.Link(temporaryPath, targetPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrAlreadyExists
		}
		return err
	}
	if err := root.Remove(temporaryPath); err != nil {
		return err
	}
	return nil
}

func applyFileInternal(ctx context.Context, root, stagingRoot *os.Root, change acfstypes.ApplyChange, create bool) error {
	relativePath, err := normalizeMutationPathInternal(change.Path)
	if err != nil {
		return err
	}
	parent, err := ensureMutationParentInternal(root, relativePath, create)
	if err != nil {
		return err
	}
	parentRoot, err := root.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer func() { _ = parentRoot.Close() }()
	base := path.Base(relativePath)

	var mode os.FileMode = applyFileMode
	if create {
		if info, err := parentRoot.Lstat(base); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return ErrSymlink
			}
			return ErrAlreadyExists
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else {
		info, err := parentRoot.Lstat(base)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlink
		}
		if !info.Mode().IsRegular() {
			return ErrNotFile
		}
		mode = info.Mode() & chmodModeMask
	}

	staged, err := openStagedFileInternal(stagingRoot, change)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close() }()

	if create {
		return copyCreateFileInternal(ctx, parentRoot, base, staged, change.Size)
	}
	_, err = writeFromRootInternal(ctx, parentRoot, change.Path, base, staged, change.Size, mode)
	return err
}

func applyCreateFolderInternal(root *os.Root, logicalPath string) error {
	relativePath, err := normalizeMutationPathInternal(logicalPath)
	if err != nil {
		return err
	}
	parent, err := ensureMutationParentInternal(root, relativePath, true)
	if err != nil {
		return err
	}
	parentRoot, err := root.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer func() { _ = parentRoot.Close() }()
	base := path.Base(relativePath)
	if info, err := parentRoot.Lstat(base); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlink
		}
		return ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := parentRoot.Mkdir(base, applyDirectoryMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func applyRenameInternal(root *os.Root, sourcePath, targetPath string) error {
	source, err := normalizeMutationPathInternal(sourcePath)
	if err != nil {
		return err
	}
	target, err := normalizeMutationPathInternal(targetPath)
	if err != nil {
		return err
	}
	if source == target || strings.HasPrefix(target, source+"/") {
		return fmt.Errorf("%w: invalid destination", ErrInvalidPath)
	}
	if _, err := inspectMutationPathInternal(root, source); err != nil {
		return err
	}
	if _, err := ensureMutationParentInternal(root, target, false); err != nil {
		return err
	}
	if info, err := root.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlink
		}
		return ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return root.Rename(source, target)
}

func applyDeleteInternal(root *os.Root, logicalPath string, recursive bool) error {
	if logicalPath == "/" {
		return ErrRootRemoval
	}
	relativePath, err := normalizeMutationPathInternal(logicalPath)
	if err != nil {
		return err
	}
	parent, err := ensureMutationParentInternal(root, relativePath, false)
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
	if err := parentRoot.Remove(base); err != nil {
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return ErrNotEmpty
		}
		return err
	}
	return nil
}

func validateApplyChangeInternal(change acfstypes.ApplyChange) error {
	if change.Path == "/" && change.Operation == acfstypes.ApplyDelete {
		return ErrRootRemoval
	}
	if _, err := normalizeMutationPathInternal(change.Path); err != nil {
		return err
	}
	switch change.Operation {
	case acfstypes.ApplyCreateFile, acfstypes.ApplyUpdateFile:
		if change.TargetPath != "" || change.Recursive || change.Size < 0 {
			return fmt.Errorf("%w: operation has unexpected fields", ErrInvalidPath)
		}
		return validateStagedNameInternal(change.StagedName)
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
		if _, err := normalizeMutationPathInternal(change.TargetPath); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported operation %q", ErrInvalidPath, change.Operation)
	}
}

func pathContainsInternal(parent, child string) (bool, error) {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))), nil
}

func validateDistinctRootsInternal(rootPath, stagingPath string) error {
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	staging, err := filepath.EvalSymlinks(stagingPath)
	if err != nil {
		return fmt.Errorf("resolve staging root: %w", err)
	}
	stagingInsideRoot, err := pathContainsInternal(root, staging)
	if err != nil {
		return err
	}
	rootInsideStaging, err := pathContainsInternal(staging, root)
	if err != nil {
		return err
	}
	if stagingInsideRoot || rootInsideStaging {
		return fmt.Errorf("%w: workspace and staging roots overlap", ErrInvalidPath)
	}
	return nil
}

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
		if err := validateApplyChangeInternal(change); err != nil {
			return 0, &acfstypes.ApplyError{Index: index, Operation: change.Operation, Path: change.Path, Err: err}
		}
	}
	if err := validateDistinctRootsInternal(rootPath, stagingPath); err != nil {
		return 0, err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return 0, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()
	stagingRoot, err := os.OpenRoot(stagingPath)
	if err != nil {
		return 0, fmt.Errorf("open staging root: %w", err)
	}
	defer func() { _ = stagingRoot.Close() }()

	for index, change := range manifest.Changes {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		var applyErr error
		switch change.Operation {
		case acfstypes.ApplyCreateFile:
			applyErr = applyFileInternal(ctx, root, stagingRoot, change, true)
		case acfstypes.ApplyUpdateFile:
			applyErr = applyFileInternal(ctx, root, stagingRoot, change, false)
		case acfstypes.ApplyCreateFolder:
			applyErr = applyCreateFolderInternal(root, change.Path)
		case acfstypes.ApplyRename, acfstypes.ApplyMove:
			applyErr = applyRenameInternal(root, change.Path, change.TargetPath)
		case acfstypes.ApplyDelete:
			applyErr = applyDeleteInternal(root, change.Path, change.Recursive)
		}
		if applyErr != nil {
			return index, &acfstypes.ApplyError{Index: index, Operation: change.Operation, Path: change.Path, Err: applyErr}
		}
	}
	return len(manifest.Changes), nil
}
