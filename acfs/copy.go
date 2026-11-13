package acfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	acfstypes "kit.getarcane.app/acfs/types"
)

const (
	copyDirectoryMode = 0o755
)

// CopyTo copies r into destination, which may not nest with r. Links are skipped
// and sockets, FIFOs, and devices are recorded in CopyResult.Skipped.
func (r Root) CopyTo(ctx context.Context, destination Root, options acfstypes.CopyOptions) (acfstypes.CopyResult, error) {
	sourcePath, destinationPath, err := validateDistinctRoots(r.Path, destination.Path)
	if err != nil {
		return acfstypes.CopyResult{}, err
	}

	var skipped []string
	copied, err := copyDirectoryContents(ctx, sourcePath, destinationPath, options.TolerateUnreadable, false, func(relativePath string) {
		skipped = append(skipped, relativePath)
	})
	slices.Sort(skipped)
	return acfstypes.CopyResult{
		Skipped: skipped,
		Copied:  copied,
	}, err
}

// MirrorTo makes destination match r in place, keeping inodes of bind-mounted
// files and tolerating unreadable or unwritable entries (#3509, #3625).
func (r Root) MirrorTo(ctx context.Context, destination Root, options acfstypes.MirrorOptions) error {
	sourcePath, destinationPath, err := validateDistinctRoots(r.Path, destination.Path)
	if err != nil {
		return err
	}

	err = pruneMirrorDestination(ctx, sourcePath, destinationPath, options.Preserve)
	if err != nil {
		return err
	}

	_, err = copyDirectoryContents(ctx, sourcePath, destinationPath, true, true, func(string) {})
	return err
}

func pruneMirrorDestination(ctx context.Context, sourcePath, destinationPath string, preserved []string) error {
	preserve := make(map[string]struct{}, len(preserved))
	for _, entry := range preserved {
		preserve[filepath.Clean(entry)] = struct{}{}
	}
	sourceRoot, openErr := os.OpenRoot(sourcePath)
	if openErr != nil {
		return openErr
	}
	defer func() {
		_ = sourceRoot.Close()
	}()

	mutation, openErr := openMutationRoot(destinationPath)
	if openErr != nil {
		return openErr
	}
	defer func() {
		_ = mutation.Close()
	}()

	return filepath.WalkDir(destinationPath, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrPermission) || errors.Is(walkErr, os.ErrNotExist) {
			return keepUnprunableEntry(entry)
		}
		if walkErr != nil {
			return walkErr
		}
		if currentPath == destinationPath {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		relativePath, err := filepath.Rel(destinationPath, currentPath)
		if err != nil {
			return err
		}
		if _, ok := preserve[relativePath]; ok {
			return keepUnprunableEntry(entry)
		}

		sourceInfo, sourceErr := sourceRoot.Lstat(relativePath)
		if sourceErr == nil && sourceInfo.Mode()&os.ModeType == entry.Type() {
			return nil
		}
		if errors.Is(sourceErr, os.ErrPermission) {
			return keepUnprunableEntry(entry)
		}
		if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
			return sourceErr
		}
		if entry.IsDir() && slices.ContainsFunc(slices.Collect(maps.Keys(preserve)), func(preserved string) bool {
			return strings.HasPrefix(preserved, relativePath+string(filepath.Separator))
		}) {
			return nil
		}

		parent, base, err := mutation.openParent(filepath.ToSlash(relativePath), false)
		if err != nil {
			return err
		}
		err = errors.Join(parent.RemoveAll(base), parent.Close())
		if err != nil {
			return err
		}
		return keepUnprunableEntry(entry)
	})
}

func copyDirectoryContents(ctx context.Context, sourceDir, destinationDir string, tolerate, mirror bool, record func(relativePath string)) (int, error) {
	sourceRoot, openErr := os.OpenRoot(sourceDir)
	if openErr != nil {
		return 0, openErr
	}
	defer func() {
		_ = sourceRoot.Close()
	}()

	destination, openErr := openMutationRoot(destinationDir)
	if openErr != nil {
		return 0, openErr
	}
	defer func() {
		_ = destination.Close()
	}()

	copied := 0
	err := filepath.WalkDir(sourceDir, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if !tolerate || (!errors.Is(walkErr, os.ErrPermission) && !errors.Is(walkErr, os.ErrNotExist)) {
				return walkErr
			}
			if relativePath, relErr := filepath.Rel(sourceDir, currentPath); relErr == nil {
				record(relativePath)
			}
			return keepUnprunableEntry(entry)
		}
		if currentPath == sourceDir {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), temporaryWritePrefix) {
			return keepUnprunableEntry(entry)
		}

		relativePath, err := filepath.Rel(sourceDir, currentPath)
		if err != nil {
			return err
		}

		switch {
		case entry.IsDir():
			target := filepath.ToSlash(relativePath)
			created, createErr := destination.top.openDir(target, dirOptions{Create: true, Mode: copyDirectoryMode})
			if pathErr, ok := errors.AsType[*fs.PathError](createErr); ok && pathErr.Path == target && errors.Is(pathErr.Err, syscall.ENOTDIR) {
				return &fs.PathError{Op: "mkdirat", Path: target, Err: syscall.EEXIST}
			}
			if createErr != nil {
				return createErr
			}
			return created.Close()
		case entry.Type()&os.ModeSymlink != 0:
			return nil
		case !entry.Type().IsRegular():
			// Reading a socket, FIFO, or device fails with ENXIO (#3003).
			record(relativePath)
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		content, err := sourceRoot.ReadFile(relativePath)
		if tolerate && (errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist)) {
			record(relativePath)
			return nil
		}
		if err != nil {
			return err
		}

		err = writeCopiedFile(destination, filepath.ToSlash(relativePath), content, info.Mode().Perm()|info.Mode()&os.ModeSticky, mirror)
		if mirror && errors.Is(err, os.ErrPermission) {
			record(relativePath)
			return nil
		}
		if err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

// writeCopiedFile writes a copied file in place without following symbolic
// links. In mirror mode an identical destination file is left untouched.
func writeCopiedFile(destination *mutationRoot, relativePath string, content []byte, mode os.FileMode, mirror bool) (retErr error) {
	parent, err := destination.top.openDir(path.Dir(relativePath), dirOptions{Create: true, Mode: copyDirectoryMode})
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, parent.Close())
	}()
	base := path.Base(relativePath)

	if mirror {
		if existing, readErr := parent.ReadFile(base); readErr == nil && bytes.Equal(existing, content) {
			return nil
		}
	}
	file, err := parent.OpenFile(base, os.O_WRONLY|os.O_CREATE, mode)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s", ErrNotFile, parent.display(base))
	}
	if info.Size() > 0 {
		err = file.Truncate(0)
		if err != nil {
			return err
		}
	}
	_, err = file.Write(content)
	return err
}

// keepUnprunableEntry skips an entry that must stay during a walk.
func keepUnprunableEntry(entry os.DirEntry) error {
	if entry != nil && entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
}
