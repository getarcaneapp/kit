package acfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"

	"go.getarcane.app/acfs/pkg/utils"
	acfstypes "go.getarcane.app/acfs/types"
)

const directoryReadBatchSize = 256

func listNamesInternal(ctx context.Context, root *os.Root, resolvedPath, logicalPath string) ([]string, error) {
	directory, err := root.Open(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("open directory %q: %w", logicalPath, err)
	}
	defer func() { _ = directory.Close() }()

	names := make([]string, 0, directoryReadBatchSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := directory.ReadDir(directoryReadBatchSize)
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read directory %q: %w", logicalPath, readErr)
		}
	}
	slices.Sort(names)
	return names, nil
}

// ListEach visits the direct children of a root-confined directory in
// deterministic name order without retaining all metadata in memory.
func ListEach(ctx context.Context, rootPath, logicalPath string, visit func(acfstypes.Entry) error) error {
	if visit == nil {
		return errors.New("visit callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePathInternal(root, relativePath, true)
	if err != nil {
		return err
	}

	names, err := listNamesInternal(ctx, root, resolvedPath, logicalPath)
	if err != nil {
		return err
	}

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}

		entryPath := name
		if resolvedPath != "." {
			entryPath = resolvedPath + "/" + name
		}
		info, err := root.Lstat(entryPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat %q: %w", utils.LogicalPath(entryPath), err)
		}

		entry, err := entryFromInfoInternal(root, entryPath, info)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if err := visit(entry); err != nil {
			return fmt.Errorf("visit %q: %w", entry.Path, err)
		}
	}
	return nil
}

// List returns the direct children of a root-confined directory.
func List(ctx context.Context, rootPath, logicalPath string) ([]acfstypes.Entry, error) {
	entries := make([]acfstypes.Entry, 0)
	err := ListEach(ctx, rootPath, logicalPath, func(entry acfstypes.Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Stat returns metadata for a root-confined path without following its final
// symbolic link.
func Stat(ctx context.Context, rootPath, logicalPath string) (acfstypes.Entry, error) {
	if err := ctx.Err(); err != nil {
		return acfstypes.Entry{}, err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return acfstypes.Entry{}, err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePathInternal(root, relativePath, false)
	if err != nil {
		return acfstypes.Entry{}, err
	}
	info, err := root.Lstat(resolvedPath)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("stat %q: %w", logicalPath, err)
	}

	return entryFromInfoInternal(root, resolvedPath, info)
}

// Walk visits every descendant of a root-confined directory in deterministic
// depth-first order. It does not follow symbolic links encountered in the tree.
func Walk(ctx context.Context, rootPath, logicalPath string, visit func(acfstypes.Entry) error) error {
	if visit == nil {
		return errors.New("visit callback is required")
	}

	directories := []string{logicalPath}
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}

		last := len(directories) - 1
		currentDirectory := directories[last]
		directories = directories[:last]

		childDirectories := make([]string, 0)
		err := ListEach(ctx, rootPath, currentDirectory, func(entry acfstypes.Entry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(entry); err != nil {
				return err
			}
			if entry.IsDirectory && !entry.IsSymlink {
				childDirectories = append(childDirectories, entry.Path)
			}
			return nil
		})
		if err != nil {
			return err
		}

		for _, childDirectory := range slices.Backward(childDirectories) {
			directories = append(directories, childDirectory)
		}
	}

	return nil
}

// MkdirAll creates a root-confined directory and any missing parents.
func MkdirAll(ctx context.Context, rootPath, logicalPath string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode != mode.Perm() {
		return fmt.Errorf("invalid directory mode %v", mode)
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolveForMkdirInternal(root, relativePath)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(resolvedPath, mode); err != nil {
		return fmt.Errorf("create directory %q: %w", logicalPath, err)
	}
	return nil
}

// RemoveAll removes a root-confined file, symbolic link, or directory tree.
func RemoveAll(ctx context.Context, rootPath, logicalPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return ErrRootRemoval
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParentInternal(root, relativePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	targetPath := path.Join(resolvedParent, base)
	if err := root.RemoveAll(targetPath); err != nil {
		return fmt.Errorf("remove %q: %w", logicalPath, err)
	}
	return nil
}
