package acfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

type directoryCopy struct {
	ctx         context.Context
	destination *mutationRoot
	record      func(relativePath string)
	// buffer holds the two chunks mirror mode compares.
	buffer   []byte
	tolerate bool
	mirror   bool
	copied   int
}

const (
	copyDirectoryMode = 0o755
	compareChunkSize  = 64 << 10
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

	c := &directoryCopy{
		ctx:         ctx,
		destination: destination,
		record:      record,
		buffer:      make([]byte, 2*compareChunkSize),
		tolerate:    tolerate,
		mirror:      mirror,
	}
	err := c.copyDirectory(sourceRoot, ".")
	return c.copied, err
}

// copyDirectory copies the children of source, found at relativePath. Names and
// metadata come from source's descriptor, so a directory swapped for a link
// mid-copy is never listed from outside the root.
func (c *directoryCopy) copyDirectory(source *os.Root, relativePath string) error {
	tolerable := func(err error) bool {
		return c.tolerate && (errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist))
	}
	directory, err := source.Open(".")
	if err != nil {
		if tolerable(err) {
			c.record(relativePath)
			return nil
		}
		return err
	}
	names, err := directory.Readdirnames(-1)
	_ = directory.Close()
	if tolerable(err) {
		c.record(relativePath)
		return nil
	}
	if err != nil {
		return err
	}
	slices.Sort(names)

	for _, name := range names {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(name, temporaryWritePrefix) {
			continue
		}
		childPath := filepath.Join(relativePath, name)
		info, err := source.Lstat(name)
		if tolerable(err) {
			c.record(childPath)
			continue
		}
		if err != nil {
			return err
		}

		switch {
		case info.IsDir():
			target := filepath.ToSlash(childPath)
			created, createErr := c.destination.top.openDir(target, dirOptions{Create: true, Mode: copyDirectoryMode})
			if pathErr, ok := errors.AsType[*fs.PathError](createErr); ok && pathErr.Path == target && errors.Is(pathErr.Err, syscall.ENOTDIR) {
				return &fs.PathError{
					Op:   "mkdirat",
					Path: target,
					Err:  syscall.EEXIST,
				}
			}
			if createErr != nil {
				return createErr
			}
			if err := created.Close(); err != nil {
				return err
			}
			child, err := source.OpenRoot(name)
			if tolerable(err) {
				c.record(childPath)
				continue
			}
			if err != nil {
				return err
			}
			err = errors.Join(c.copyDirectory(child, childPath), child.Close())
			if err != nil {
				return err
			}
		case info.Mode()&os.ModeSymlink != 0:
		case !info.Mode().IsRegular():
			// Reading a socket, FIFO, or device fails with ENXIO (#3003).
			c.record(childPath)
		default:
			file, err := source.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if tolerable(err) {
				c.record(childPath)
				continue
			}
			if err != nil {
				return err
			}
			regular, err := c.copyFile(file, filepath.ToSlash(childPath), info.Mode().Perm()|info.Mode()&os.ModeSticky)
			if !regular || (c.mirror && errors.Is(err, os.ErrPermission)) {
				c.record(childPath)
				continue
			}
			if err != nil {
				return err
			}
			c.copied++
		}
	}
	return nil
}

// copyFile copies source, closing it, to relativePath in place without
// following symbolic links, and reports false if source is no longer a regular
// file. In mirror mode an identical destination file is left untouched.
func (c *directoryCopy) copyFile(source *os.File, relativePath string, mode os.FileMode) (regular bool, retErr error) {
	defer func() {
		retErr = errors.Join(retErr, source.Close())
	}()
	sourceInfo, err := source.Stat()
	if err != nil {
		return true, err
	}
	if !sourceInfo.Mode().IsRegular() {
		return false, nil
	}

	parent, err := c.destination.top.openDir(path.Dir(relativePath), dirOptions{Create: true, Mode: copyDirectoryMode})
	if err != nil {
		return true, err
	}
	defer func() {
		retErr = errors.Join(retErr, parent.Close())
	}()
	base := path.Base(relativePath)

	if c.mirror {
		existing, openErr := parent.OpenFile(base, os.O_RDONLY, 0)
		if openErr == nil {
			existingInfo, statErr := existing.Stat()
			same := statErr == nil && existingInfo.Mode().IsRegular() && existingInfo.Size() == sourceInfo.Size()
			if same {
				same, err = equalContent(existing, source, c.buffer)
			}
			_ = existing.Close()
			if err != nil || same {
				return true, err
			}
			if _, err = source.Seek(0, io.SeekStart); err != nil {
				return true, err
			}
		}
	}
	file, err := parent.OpenFile(base, os.O_WRONLY|os.O_CREATE, mode)
	if err != nil {
		return true, err
	}
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()
	info, err := file.Stat()
	if err != nil {
		return true, err
	}
	if !info.Mode().IsRegular() {
		return true, fmt.Errorf("%w: %s", ErrNotFile, parent.display(base))
	}
	if info.Size() > 0 {
		err = file.Truncate(0)
		if err != nil {
			return true, err
		}
	}
	_, err = io.Copy(file, source)
	return true, err
}

// equalContent reports whether a and b hold the same bytes, comparing them in
// chunks of half of buffer.
func equalContent(a, b io.Reader, buffer []byte) (bool, error) {
	chunkA, chunkB := buffer[:len(buffer)/2], buffer[len(buffer)/2:]
	for {
		readA, errA := io.ReadFull(a, chunkA)
		readB, errB := io.ReadFull(b, chunkB)
		endA := errors.Is(errA, io.EOF) || errors.Is(errA, io.ErrUnexpectedEOF)
		endB := errors.Is(errB, io.EOF) || errors.Is(errB, io.ErrUnexpectedEOF)
		if errA != nil && !endA {
			return false, errA
		}
		if errB != nil && !endB {
			return false, errB
		}
		if !bytes.Equal(chunkA[:readA], chunkB[:readB]) {
			return false, nil
		}
		if endA || endB {
			return endA == endB, nil
		}
	}
}

// keepUnprunableEntry skips an entry that must stay during a walk.
func keepUnprunableEntry(entry os.DirEntry) error {
	if entry != nil && entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
}
