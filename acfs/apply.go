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

	kitfs "kit.getarcane.app/fs"

	acfstypes "kit.getarcane.app/acfs/types"
)

const (
	applyFileMode      = 0o644
	applyDirectoryMode = 0o755
)

// Apply performs an ordered batch of root-confined mutations, stopping at the
// first failure.
func (r Root) Apply(ctx context.Context, staging Root, manifest acfstypes.ApplyManifest) (int, error) {
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
	if _, _, err := validateDistinctRoots(r.Path, staging.Path); err != nil {
		return 0, err
	}

	mutation, openErr := openMutationRoot(r.Path)
	if openErr != nil {
		return 0, openErr
	}
	defer func() {
		_ = mutation.Close()
	}()
	stagingRoot, openErr := os.OpenRoot(staging.Path)
	if openErr != nil {
		return 0, fmt.Errorf("open staging root: %w", openErr)
	}
	defer func() {
		_ = stagingRoot.Close()
	}()

	for index, change := range manifest.Changes {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		if err := func() error {
			switch change.Operation {
			case acfstypes.ApplyCreateFile:
				return applyFile(ctx, mutation, stagingRoot, change, true)
			case acfstypes.ApplyUpdateFile:
				return applyFile(ctx, mutation, stagingRoot, change, false)
			case acfstypes.ApplyCreateFolder:
				return applyFolder(mutation, change.Path)
			case acfstypes.ApplyRename, acfstypes.ApplyMove:
				return applyRename(mutation, change.Path, change.TargetPath)
			case acfstypes.ApplyDelete:
				return applyDelete(mutation, change.Path, change.Recursive)
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

func applyFolder(mutation *mutationRoot, logicalPath string) error {
	relativePath, err := normalizeMutationPath(logicalPath)
	if err != nil {
		return err
	}
	parent, base, err := mutation.openParent(relativePath, true)
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	err = rejectExistingTarget(parent, base)
	if err != nil {
		return err
	}
	err = parent.Mkdir(base, applyDirectoryMode)
	if errors.Is(err, fs.ErrExist) {
		return ErrAlreadyExists
	}
	return err
}

func applyFile(ctx context.Context, mutation *mutationRoot, stagingRoot *os.Root, change acfstypes.ApplyChange, create bool) error {
	relativePath, err := normalizeMutationPath(change.Path)
	if err != nil {
		return err
	}
	parent, base, err := mutation.openParent(relativePath, create)
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()

	mode, err := func() (os.FileMode, error) {
		if create {
			return applyFileMode, rejectExistingTarget(parent, base)
		}
		info, lstatErr := parent.Lstat(base)
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
	defer func() {
		_ = staged.Close()
	}()

	if !create {
		_, err = writeFromRoot(ctx, parent, change.Path, base, staged, WriteFromOptions{Size: change.Size, Mode: mode})
		return err
	}

	target, temporaryName, err := createTemporaryFile(parent)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		_ = target.Close()
		if removeTemporary {
			_ = parent.Remove(temporaryName)
		}
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
	// A hard link refuses an existing name.
	if beforePublish != nil {
		beforePublish(temporaryName)
	}
	err = parent.verifyPublished(temporaryName, target)
	if err != nil {
		removeTemporary = false
		return err
	}
	err = parent.Link(temporaryName, base)
	if errors.Is(err, fs.ErrExist) {
		return ErrAlreadyExists
	}
	if err != nil {
		return err
	}
	if afterPublish != nil {
		afterPublish()
	}
	err = parent.verifyPublished(base, target)
	if err != nil {
		return err
	}
	err = target.Close()
	if err != nil {
		return err
	}
	removeTemporary = false
	return parent.Remove(temporaryName)
}

func applyRename(mutation *mutationRoot, sourcePath, targetPath string) error {
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
	sourceParent, sourceBase, err := mutation.openParent(source, false)
	if err != nil {
		return err
	}
	defer func() {
		_ = sourceParent.Close()
	}()
	info, err := sourceParent.Lstat(sourceBase)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrSymlink, kitfs.LogicalPath(source))
	}
	targetParent, targetBase, err := mutation.openParent(target, false)
	if err != nil {
		return err
	}
	defer func() {
		_ = targetParent.Close()
	}()
	err = rejectExistingTarget(targetParent, targetBase)
	if err != nil {
		return err
	}
	err = sourceParent.renameNoReplace(sourceBase, targetParent, targetBase)
	if errors.Is(err, fs.ErrExist) {
		return ErrAlreadyExists
	}
	return err
}

func applyDelete(mutation *mutationRoot, logicalPath string, recursive bool) error {
	if logicalPath == "/" {
		return ErrRootRemoval
	}
	relativePath, err := normalizeMutationPath(logicalPath)
	if err != nil {
		return err
	}
	parent, base, err := mutation.openParent(relativePath, false)
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	info, err := parent.Lstat(base)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	if info.IsDir() && recursive {
		return parent.RemoveAll(base)
	}
	err = parent.Remove(base)
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

func validateDistinctRoots(rootPath, stagingPath string) (string, string, error) {
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	staging, err := filepath.EvalSymlinks(stagingPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve staging root: %w", err)
	}
	for _, paths := range [][2]string{{root, staging}, {staging, root}} {
		relative, relErr := filepath.Rel(paths[0], paths[1])
		if relErr != nil {
			return "", "", relErr
		}
		if filepath.IsLocal(relative) {
			return "", "", fmt.Errorf("%w: workspace and staging roots overlap", ErrInvalidPath)
		}
	}
	return root, staging, nil
}
