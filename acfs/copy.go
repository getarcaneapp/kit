package acfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	acfstypes "go.getarcane.app/acfs/types"
)

const copyDirectoryMode = 0o755

type copyWalkerInternal struct {
	ctx             context.Context
	sourceDir       string
	sourceRoot      *os.Root
	destinationRoot *os.Root
	record          func(relativePath string)
	copied          int
	tolerate        bool
}

// CopyDir copies the contents of sourceRootPath into destinationRootPath. Both
// paths are separate workspace roots and may not nest, so a copy can never feed
// itself.
//
// Directories are recreated, regular files are written with the source's
// permission bits, and symbolic links are skipped silently: the destination
// gets no link, because ACFS never creates one. Sockets, FIFOs, and device
// nodes are skipped and recorded in CopyResult.Skipped.
func CopyDir(ctx context.Context, sourceRootPath, destinationRootPath string, options acfstypes.CopyOptions) (acfstypes.CopyResult, error) {
	result := acfstypes.CopyResult{}
	if err := validateDistinctRootsInternal(sourceRootPath, destinationRootPath); err != nil {
		return result, err
	}

	record := func(relativePath string) { result.Skipped = append(result.Skipped, relativePath) }
	copied, err := copyDirectoryContentsInternal(ctx, sourceRootPath, destinationRootPath, options.TolerateUnreadable, record)
	result.Copied = copied
	slices.Sort(result.Skipped)
	return result, err
}

func copyDirectoryContentsInternal(ctx context.Context, sourceDir, destinationDir string, tolerate bool, record func(relativePath string)) (int, error) {
	sourceRoot, err := os.OpenRoot(sourceDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sourceRoot.Close() }()

	destinationRoot, err := os.OpenRoot(destinationDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = destinationRoot.Close() }()

	walker := &copyWalkerInternal{
		ctx:             ctx,
		sourceDir:       sourceDir,
		sourceRoot:      sourceRoot,
		destinationRoot: destinationRoot,
		record:          record,
		tolerate:        tolerate,
	}
	err = filepath.WalkDir(sourceDir, walker.visitInternal)
	return walker.copied, err
}

func (w *copyWalkerInternal) visitInternal(currentPath string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return w.handleWalkErrorInternal(currentPath, entry, walkErr)
	}
	if currentPath == w.sourceDir {
		return nil
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if strings.HasPrefix(entry.Name(), temporaryWritePrefix) {
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}

	relativePath, err := filepath.Rel(w.sourceDir, currentPath)
	if err != nil {
		return err
	}

	if entry.IsDir() {
		return w.destinationRoot.MkdirAll(relativePath, copyDirectoryMode)
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return nil
	}
	if !entry.Type().IsRegular() {
		// Sockets, FIFOs, and device nodes left behind by running containers
		// have no copyable content — reading one fails with ENXIO, not a
		// permission error (#3003). Record them like unreadable entries so a
		// later restore preserves rather than prunes them.
		w.record(relativePath)
		return nil
	}

	return w.copyRegularFileInternal(relativePath, entry)
}

func (w *copyWalkerInternal) handleWalkErrorInternal(currentPath string, entry os.DirEntry, walkErr error) error {
	// An unreadable subdirectory surfaces here as a permission error on the
	// directory entry itself; an entry deleted mid-walk surfaces as not-exist.
	if !w.tolerate || (!errors.Is(walkErr, os.ErrPermission) && !errors.Is(walkErr, os.ErrNotExist)) {
		return walkErr
	}
	if relativePath, relErr := filepath.Rel(w.sourceDir, currentPath); relErr == nil {
		w.record(relativePath)
	}
	if entry != nil && entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

func (w *copyWalkerInternal) copyRegularFileInternal(relativePath string, entry os.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}

	content, err := w.sourceRoot.ReadFile(relativePath)
	if err != nil {
		if w.tolerate && (errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist)) {
			w.record(relativePath)
			return nil
		}
		return err
	}

	if err := w.destinationRoot.MkdirAll(filepath.Dir(relativePath), copyDirectoryMode); err != nil {
		return err
	}
	if err := w.destinationRoot.WriteFile(relativePath, content, info.Mode()&chmodModeMask); err != nil {
		return err
	}
	w.copied++
	return nil
}

// MirrorDir makes destinationRootPath match sourceRootPath while updating files
// and directories IN PLACE. Entries missing from the source, or whose type
// differs, are removed first, then the source is copied over the result.
//
// The copy phase deliberately truncates and rewrites existing destination files
// instead of using the atomic temp-then-rename of WriteFile: rename would
// replace the inode, and a mirror runs against live project directories whose
// files may be bind-mounted into running containers. Preserving inodes is the
// point — do not "fix" this to be atomic.
//
// MirrorOptions.Preserve names destination entries that must survive even
// though the source omits them; a directory holding a preserved descendant is
// descended into rather than removed wholesale. The copy phase always tolerates
// unreadable source entries, because failing half-way through leaves the live
// directory in a mixed state that is worse than one stale file (#3509, #3085).
func MirrorDir(ctx context.Context, sourceRootPath, destinationRootPath string, options acfstypes.MirrorOptions) error {
	if err := validateDistinctRootsInternal(sourceRootPath, destinationRootPath); err != nil {
		return err
	}

	preserve := make(map[string]struct{}, len(options.Preserve))
	for _, entry := range options.Preserve {
		preserve[filepath.Clean(entry)] = struct{}{}
	}
	if err := pruneDirectoryContentsInternal(ctx, sourceRootPath, destinationRootPath, preserve); err != nil {
		return err
	}

	_, err := copyDirectoryContentsInternal(ctx, sourceRootPath, destinationRootPath, true, func(string) {})
	return err
}

type pruneWalkerInternal struct {
	ctx             context.Context
	destinationDir  string
	sourceRoot      *os.Root
	destinationRoot *os.Root
	preserve        map[string]struct{}
}

func pruneDirectoryContentsInternal(ctx context.Context, sourceDir, destinationDir string, preserve map[string]struct{}) error {
	sourceRoot, err := os.OpenRoot(sourceDir)
	if err != nil {
		return err
	}
	defer func() { _ = sourceRoot.Close() }()

	destinationRoot, err := os.OpenRoot(destinationDir)
	if err != nil {
		return err
	}
	defer func() { _ = destinationRoot.Close() }()

	walker := &pruneWalkerInternal{
		ctx:             ctx,
		destinationDir:  destinationDir,
		sourceRoot:      sourceRoot,
		destinationRoot: destinationRoot,
		preserve:        preserve,
	}
	return filepath.WalkDir(destinationDir, walker.visitInternal)
}

func (w *pruneWalkerInternal) visitInternal(currentPath string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		// An unreadable destination subdirectory (e.g. foreign-owned
		// bind-mount data) cannot be pruned; leave it in place instead of
		// failing the whole mirror mid-restore (#3509, #3085). Entries that
		// vanished mid-walk need no pruning either.
		if errors.Is(walkErr, os.ErrPermission) || errors.Is(walkErr, os.ErrNotExist) {
			return keepUnprunableEntryInternal(entry)
		}
		return walkErr
	}
	if currentPath == w.destinationDir {
		return nil
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}

	relativePath, err := filepath.Rel(w.destinationDir, currentPath)
	if err != nil {
		return err
	}

	// Never delete a preserved entry.
	if _, ok := w.preserve[relativePath]; ok {
		return keepUnprunableEntryInternal(entry)
	}

	sourceInfo, sourceErr := w.sourceRoot.Lstat(relativePath)
	if sourceErr == nil && sourceInfo.Mode()&os.ModeType == entry.Type() {
		return nil
	}
	if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
		// Cannot inspect the source counterpart — keep the destination entry
		// rather than deleting something the source may still have.
		if errors.Is(sourceErr, os.ErrPermission) {
			return keepUnprunableEntryInternal(entry)
		}
		return sourceErr
	}

	// This entry is absent from (or type-changed vs) the source, so it would
	// normally be pruned. If it is a directory that still contains a preserved
	// entry, descend and prune its other children instead of removing it whole.
	if entry.IsDir() && hasPreservedDescendantInternal(relativePath, w.preserve) {
		return nil
	}

	if err := w.destinationRoot.RemoveAll(relativePath); err != nil {
		return err
	}
	if entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// keepUnprunableEntryInternal continues a prune walk past an entry that must
// stay: descend no further into directories, ignore files.
func keepUnprunableEntryInternal(entry os.DirEntry) error {
	if entry != nil && entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// hasPreservedDescendantInternal reports whether any preserved path lives under
// dir, so the prune knows to descend into dir rather than remove it wholesale.
func hasPreservedDescendantInternal(dir string, preserve map[string]struct{}) bool {
	prefix := dir + string(filepath.Separator)
	for preserved := range preserve {
		if strings.HasPrefix(preserved, prefix) {
			return true
		}
	}
	return false
}
