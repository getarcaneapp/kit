package acfs

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	acfstypes "go.getarcane.app/acfs/types"
)

const copyDirectoryMode = 0o755

// CopyDir copies the contents of sourceRootPath into destinationRootPath. Both
// paths are separate workspace roots and may not nest, so a copy can never feed
// itself.
//
// Directories are recreated, regular files are written with the source's
// permission bits, and symbolic links are skipped silently: the destination
// gets no link, because ACFS never creates one. Sockets, FIFOs, and device
// nodes are skipped and recorded in CopyResult.Skipped.
func CopyDir(ctx context.Context, sourceRootPath, destinationRootPath string, options acfstypes.CopyOptions) (acfstypes.CopyResult, error) {
	if err := validateDistinctRoots(sourceRootPath, destinationRootPath); err != nil {
		return acfstypes.CopyResult{}, err
	}

	var skipped []string
	copied, err := copyDirectoryContents(ctx, sourceRootPath, destinationRootPath, options.TolerateUnreadable, false, func(relativePath string) {
		skipped = append(skipped, relativePath)
	})
	slices.Sort(skipped)
	return acfstypes.CopyResult{
		Skipped: skipped,
		Copied:  copied,
	}, err
}

// MirrorDir makes destinationRootPath match sourceRootPath while updating files
// and directories IN PLACE. Entries missing from the source, or whose type
// differs, are removed first, then the source is copied over the result.
//
// The copy phase deliberately truncates and rewrites existing destination files
// instead of using the default atomic temp-then-rename of Write: rename would
// replace the inode, and a mirror runs against live project directories whose
// files may be bind-mounted into running containers. Preserving inodes is the
// point — do not "fix" this to be atomic. A destination file whose content
// already matches the source is left entirely untouched (inode and mtime
// preserved).
//
// MirrorOptions.Preserve names destination entries that must survive even
// though the source omits them; a directory holding a preserved descendant is
// descended into rather than removed wholesale. The copy phase always tolerates
// unreadable source entries, because failing half-way through leaves the live
// directory in a mixed state that is worse than one stale file (#3509, #3085).
// Destination files the process cannot write are tolerated in the same spirit
// (#3625): the stale file keeps its old content and the mirror carries on.
func MirrorDir(ctx context.Context, sourceRootPath, destinationRootPath string, options acfstypes.MirrorOptions) error {
	if err := validateDistinctRoots(sourceRootPath, destinationRootPath); err != nil {
		return err
	}

	preserve := make(map[string]struct{}, len(options.Preserve))
	for _, entry := range options.Preserve {
		preserve[filepath.Clean(entry)] = struct{}{}
	}
	if err := func() error {
		sourceRoot, openErr := os.OpenRoot(sourceRootPath)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = sourceRoot.Close() }()

		destinationRoot, openErr := os.OpenRoot(destinationRootPath)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = destinationRoot.Close() }()

		return filepath.WalkDir(destinationRootPath, func(currentPath string, entry os.DirEntry, walkErr error) error {
			// An unreadable destination subdirectory (e.g. foreign-owned
			// bind-mount data) cannot be pruned; leave it in place instead of
			// failing the whole mirror mid-restore (#3509, #3085). Entries that
			// vanished mid-walk need no pruning either.
			if errors.Is(walkErr, os.ErrPermission) || errors.Is(walkErr, os.ErrNotExist) {
				return keepUnprunableEntry(entry)
			}
			if walkErr != nil {
				return walkErr
			}
			if currentPath == destinationRootPath {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			relativePath, err := filepath.Rel(destinationRootPath, currentPath)
			if err != nil {
				return err
			}

			// Never delete a preserved entry.
			if _, ok := preserve[relativePath]; ok {
				return keepUnprunableEntry(entry)
			}

			sourceInfo, sourceErr := sourceRoot.Lstat(relativePath)
			if sourceErr == nil && sourceInfo.Mode()&os.ModeType == entry.Type() {
				return nil
			}
			// Cannot inspect the source counterpart — keep the destination entry
			// rather than deleting something the source may still have.
			if errors.Is(sourceErr, os.ErrPermission) {
				return keepUnprunableEntry(entry)
			}
			if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
				return sourceErr
			}

			// This entry is absent from (or type-changed vs) the source, so it would
			// normally be pruned. If it is a directory that still contains a preserved
			// entry, descend and prune its other children instead of removing it whole.
			if entry.IsDir() && slices.ContainsFunc(slices.Collect(maps.Keys(preserve)), func(preserved string) bool {
				return strings.HasPrefix(preserved, relativePath+string(filepath.Separator))
			}) {
				return nil
			}

			err = destinationRoot.RemoveAll(relativePath)
			if err != nil {
				return err
			}
			return keepUnprunableEntry(entry)
		})
	}(); err != nil {
		return err
	}

	_, err := copyDirectoryContents(ctx, sourceRootPath, destinationRootPath, true, true, func(string) {})
	return err
}

func copyDirectoryContents(ctx context.Context, sourceDir, destinationDir string, tolerate, mirror bool, record func(relativePath string)) (int, error) {
	sourceRoot, openErr := os.OpenRoot(sourceDir)
	if openErr != nil {
		return 0, openErr
	}
	defer func() { _ = sourceRoot.Close() }()

	destinationRoot, openErr := os.OpenRoot(destinationDir)
	if openErr != nil {
		return 0, openErr
	}
	defer func() { _ = destinationRoot.Close() }()

	copied := 0
	err := filepath.WalkDir(sourceDir, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// An unreadable subdirectory surfaces here as a permission error on the
			// directory entry itself; an entry deleted mid-walk surfaces as not-exist.
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
			return destinationRoot.MkdirAll(relativePath, copyDirectoryMode)
		case entry.Type()&os.ModeSymlink != 0:
			return nil
		case !entry.Type().IsRegular():
			// Sockets, FIFOs, and device nodes left behind by running containers
			// have no copyable content — reading one fails with ENXIO, not a
			// permission error (#3003). Record them like unreadable entries so a
			// later restore preserves rather than prunes them.
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

		// In mirror mode, leave an already-identical destination file untouched:
		// the in-place rewrite would only churn its mtime, and it would fail
		// outright on a file the process can read but not write (#3625).
		if mirror {
			if existing, readErr := destinationRoot.ReadFile(relativePath); readErr == nil && bytes.Equal(existing, content) {
				return nil
			}
		}

		err = destinationRoot.MkdirAll(filepath.Dir(relativePath), copyDirectoryMode)
		if err != nil {
			return err
		}
		err = destinationRoot.WriteFile(relativePath, content, info.Mode()&chmodModeMask)
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

// keepUnprunableEntry continues a prune walk past an entry that must
// stay: descend no further into directories, ignore files.
func keepUnprunableEntry(entry os.DirEntry) error {
	if entry != nil && entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
}
