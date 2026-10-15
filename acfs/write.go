package acfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	kitfs "go.getarcane.app/kit/pkg/fs"
)

const (
	temporaryWritePrefix = ".acfs-write-"
	chmodModeMask        = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
)

// WriteOptions configures the permissions and replacement behavior of a write.
type WriteOptions struct {
	Mode os.FileMode
	// InPlace preserves existing regular-file inodes and refuses final symlinks.
	// The default uses an atomic replacement; in-place writes can be partial on failure.
	InPlace bool
}

// WriteFrom atomically writes exactly expectedSize bytes from source to a
// root-confined file. The destination is unchanged when the transfer fails.
func WriteFrom(ctx context.Context, rootPath, logicalPath string, source io.Reader, expectedSize int64, mode os.FileMode) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if source == nil {
		return 0, errors.New("source reader is required")
	}
	if expectedSize < 0 {
		return 0, fmt.Errorf("%w: size must be non-negative", ErrSizeMismatch)
	}
	if mode&^chmodModeMask != 0 {
		return 0, fmt.Errorf("invalid file mode %v", mode)
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return 0, err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return 0, err
	}
	if relativePath == "." {
		return 0, ErrIsDirectory
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return 0, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedParent, base, err := resolveParent(root, relativePath)
	if err != nil {
		return 0, err
	}
	return writeFromRoot(ctx, root, logicalPath, path.Join(resolvedParent, base), source, expectedSize, mode)
}

// Write writes data to a root-confined file, creating it when absent. By default
// it atomically replaces the destination, including a final symbolic link.
// With InPlace enabled it preserves regular-file inodes, leaves identical files
// untouched, and refuses final symlinks. In-place writes are not atomic; callers
// coordinate writers and provide recovery if partial writes must be rolled back.
func Write(ctx context.Context, rootPath, logicalPath string, data []byte, options WriteOptions) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.Mode&^chmodModeMask != 0 {
		return fmt.Errorf("invalid file mode %v", options.Mode)
	}
	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return ErrIsDirectory
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()
	parent, base, err := resolveParent(root, relativePath)
	if err != nil {
		return err
	}
	targetPath := path.Join(parent, base)
	err = rejectReservedPath(targetPath)
	if err != nil {
		return err
	}
	if !options.InPlace {
		_, err = writeFromRoot(ctx, root, logicalPath, targetPath, bytes.NewReader(data), int64(len(data)), options.Mode)
		return err
	}
	info, err := root.Lstat(targetPath)
	flags := os.O_WRONLY
	identical := false
	switch {
	case errors.Is(err, os.ErrNotExist):
		flags |= os.O_CREATE | os.O_EXCL
	case err != nil:
		return fmt.Errorf("inspect %q: %w", kitfs.LogicalPath(targetPath), err)
	case info.Mode()&os.ModeSymlink != 0:
		return ErrSymlink
	case info.IsDir():
		return ErrIsDirectory
	case !info.Mode().IsRegular():
		return ErrNotFile
	default:
		existing, readErr := root.ReadFile(targetPath)
		if readErr != nil {
			return fmt.Errorf("read %q: %w", kitfs.LogicalPath(targetPath), readErr)
		}
		identical = bytes.Equal(existing, data)
		if identical && info.Mode()&chmodModeMask == options.Mode {
			return ctx.Err()
		}
	}

	file, err := root.OpenFile(targetPath, flags, options.Mode)
	if err != nil {
		return fmt.Errorf("open %q: %w", kitfs.LogicalPath(targetPath), err)
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened file: %w", err)
	}
	live, err := root.Lstat(targetPath)
	if err != nil {
		return fmt.Errorf("stat destination: %w", err)
	}
	if live.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	if !opened.Mode().IsRegular() || !live.Mode().IsRegular() || !os.SameFile(opened, live) || (info != nil && !os.SameFile(info, opened)) {
		return fmt.Errorf("%w: destination changed before writing", ErrNotFile)
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	if !identical {
		err = file.Truncate(0)
		if err != nil {
			return fmt.Errorf("truncate %q: %w", kitfs.LogicalPath(targetPath), err)
		}
		_, err = file.Write(data)
		if err != nil {
			return fmt.Errorf("write %q: %w", kitfs.LogicalPath(targetPath), err)
		}
	}
	if !identical || opened.Mode()&chmodModeMask != options.Mode {
		err = file.Chmod(options.Mode)
		if err != nil {
			return fmt.Errorf("chmod %q: %w", kitfs.LogicalPath(targetPath), err)
		}
	}
	return nil
}

// WriteAt writes data to an existing root-confined regular file at the given
// byte offset, growing the file (sparsely) when the offset lies beyond its
// current end. Unlike WriteFrom, the write is in-place and not atomic; callers
// coordinate concurrent writers themselves.
func WriteAt(ctx context.Context, rootPath, logicalPath string, offset int64, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if offset < 0 {
		return fmt.Errorf("%w: offset must be non-negative", ErrInvalidPath)
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return ErrIsDirectory
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePath(root, relativePath, true)
	if err != nil {
		return err
	}

	file, err := root.OpenFile(resolvedPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %q: %w", kitfs.LogicalPath(resolvedPath), err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", kitfs.LogicalPath(resolvedPath), err)
	}
	if !info.Mode().IsRegular() {
		return ErrNotFile
	}

	_, err = file.WriteAt(data, offset)
	if err != nil {
		return fmt.Errorf("write %q at offset %d: %w", logicalPath, offset, err)
	}
	return nil
}

func rejectReservedPath(relativePath string) error {
	for component := range strings.SplitSeq(relativePath, "/") {
		if strings.HasPrefix(component, temporaryWritePrefix) {
			return fmt.Errorf("%w: %q uses reserved ACFS name", ErrInvalidPath, component)
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func createTemporaryFile(root *os.Root, parent string) (*os.File, string, error) {
	var randomBytes [16]byte
	for range 10 {
		if _, err := rand.Read(randomBytes[:]); err != nil {
			return nil, "", fmt.Errorf("generate temporary filename: %w", err)
		}
		filename := temporaryWritePrefix + hex.EncodeToString(randomBytes[:])
		temporaryPath := path.Join(parent, filename)
		file, err := root.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return file, temporaryPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", fmt.Errorf("create temporary file: %w", err)
		}
	}
	return nil, "", errors.New("could not allocate a unique temporary file")
}

func writeFromRoot(ctx context.Context, root *os.Root, logicalPath, targetPath string, source io.Reader, expectedSize int64, mode os.FileMode) (int64, error) {
	temporaryFile, temporaryPath, err := createTemporaryFile(root, path.Dir(targetPath))
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		_ = temporaryFile.Close()
		if !committed {
			_ = root.Remove(temporaryPath)
		}
	}()

	reader := contextReader{ctx: ctx, reader: source}
	written, err := io.CopyN(temporaryFile, reader, expectedSize)
	if err != nil {
		return written, fmt.Errorf("%w: expected %d bytes, received %d: %w", ErrSizeMismatch, expectedSize, written, err)
	}

	var extra [1]byte
	extraCount, extraErr := io.ReadFull(reader, extra[:])
	if extraCount != 0 {
		return written, fmt.Errorf("%w: input exceeds %d bytes", ErrSizeMismatch, expectedSize)
	}
	if extraErr != nil && !errors.Is(extraErr, io.EOF) {
		return written, fmt.Errorf("check input size: %w", extraErr)
	}

	err = temporaryFile.Chmod(mode)
	if err != nil {
		return written, fmt.Errorf("chmod temporary file: %w", err)
	}
	err = temporaryFile.Sync()
	if err != nil {
		return written, fmt.Errorf("sync temporary file: %w", err)
	}
	err = temporaryFile.Close()
	if err != nil {
		return written, fmt.Errorf("close temporary file: %w", err)
	}
	err = root.Rename(temporaryPath, targetPath)
	if err != nil {
		return written, fmt.Errorf("replace %q: %w", logicalPath, err)
	}

	committed = true
	return written, nil
}
