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

	"go.getarcane.app/acfs/pkg/utils"
)

const temporaryWritePrefix = ".acfs-write-"

const chmodModeMask = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky

func rejectReservedPathInternal(relativePath string) error {
	for component := range strings.SplitSeq(relativePath, "/") {
		if strings.HasPrefix(component, temporaryWritePrefix) {
			return fmt.Errorf("%w: %q uses reserved ACFS name", ErrInvalidPath, component)
		}
	}
	return nil
}

type contextReaderInternal struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReaderInternal) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func createTemporaryFileInternal(root *os.Root, parent string) (*os.File, string, error) {
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

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return 0, err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
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

	resolvedParent, base, err := resolveParentInternal(root, relativePath)
	if err != nil {
		return 0, err
	}
	targetPath := path.Join(resolvedParent, base)
	return writeFromRootInternal(ctx, root, logicalPath, targetPath, source, expectedSize, mode)
}

// WriteFile atomically writes data to a root-confined file, creating it when
// absent. Like WriteFrom, the write lands on a temporary file in the target's
// directory that is renamed into place, so a reader never observes a partial
// file and a failure leaves the previous contents intact.
//
// A final component that is a symbolic link is replaced by the new regular
// file rather than written through. Callers that must honour an existing
// symlink resolve it themselves first.
func WriteFile(ctx context.Context, rootPath, logicalPath string, data []byte, mode os.FileMode) error {
	_, err := WriteFrom(ctx, rootPath, logicalPath, bytes.NewReader(data), int64(len(data)), mode)
	return err
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

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	if err := rejectReservedPathInternal(relativePath); err != nil {
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

	resolvedPath, err := resolvePathInternal(root, relativePath, true)
	if err != nil {
		return err
	}

	file, err := root.OpenFile(resolvedPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %q: %w", utils.LogicalPath(resolvedPath), err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", utils.LogicalPath(resolvedPath), err)
	}
	if !info.Mode().IsRegular() {
		return ErrNotFile
	}

	if _, err := file.WriteAt(data, offset); err != nil {
		return fmt.Errorf("write %q at offset %d: %w", logicalPath, offset, err)
	}
	return nil
}

func writeFromRootInternal(ctx context.Context, root *os.Root, logicalPath, targetPath string, source io.Reader, expectedSize int64, mode os.FileMode) (int64, error) {
	resolvedParent := path.Dir(targetPath)

	temporaryFile, temporaryPath, err := createTemporaryFileInternal(root, resolvedParent)
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

	reader := contextReaderInternal{ctx: ctx, reader: source}
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

	if err := temporaryFile.Chmod(mode); err != nil {
		return written, fmt.Errorf("chmod temporary file: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		return written, fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return written, fmt.Errorf("close temporary file: %w", err)
	}
	if err := root.Rename(temporaryPath, targetPath); err != nil {
		return written, fmt.Errorf("replace %q: %w", logicalPath, err)
	}

	committed = true
	return written, nil
}
