package acfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// WriteFrom atomically writes exactly options.Size bytes from source to a host file.
func (Host) WriteFrom(ctx context.Context, hostPath string, source io.Reader, options WriteFromOptions) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if source == nil {
		return 0, errors.New("source reader is required")
	}
	if options.Size < 0 {
		return 0, fmt.Errorf("%w: size must be non-negative", ErrSizeMismatch)
	}
	if options.Mode&^chmodModeMask != 0 {
		return 0, fmt.Errorf("invalid file options.Mode %v", options.Mode)
	}
	targetPath, err := cleanHostFilePath(hostPath)
	if err != nil {
		return 0, err
	}
	parent, err := openHostDir(filepath.Dir(targetPath))
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = parent.Close()
	}()
	return writeFromRoot(ctx, parent, targetPath, filepath.Base(targetPath), source, options)
}

// Write writes data to a host file like Root.Write.
func (Host) Write(ctx context.Context, hostPath string, data []byte, options WriteOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := options.validate(); err != nil {
		return err
	}
	targetPath, err := cleanHostFilePath(hostPath)
	if err != nil {
		return err
	}
	parent, err := openHostDir(filepath.Dir(targetPath))
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	if !options.InPlace {
		_, err = writeFromRoot(ctx, parent, targetPath, filepath.Base(targetPath), bytes.NewReader(data), WriteFromOptions{
			Size:      int64(len(data)),
			Mode:      options.Mode,
			Exclusive: options.Exclusive,
		})
		return err
	}
	return writeInPlace(ctx, parent, filepath.Base(targetPath), data, options.Mode)
}

// WriteAt writes data at offset into an existing host file like Root.WriteAt.
func (Host) WriteAt(ctx context.Context, hostPath string, offset int64, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if offset < 0 {
		return fmt.Errorf("%w: offset must be non-negative", ErrInvalidPath)
	}
	targetPath, err := cleanHostFilePath(hostPath)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(targetPath, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return openError(err, targetPath)
	}
	return writeAtAndClose(file, targetPath, offset, data)
}

// Rename moves source onto target like Root.Rename.
func (Host) Rename(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sourcePath, err := cleanHostPath(source)
	if err != nil {
		return err
	}
	targetPath, err := cleanHostPath(target)
	if err != nil {
		return err
	}
	// A filesystem root, including a drive or UNC share root, is its own parent.
	if filepath.Dir(sourcePath) == sourcePath {
		return ErrRootRemoval
	}
	if filepath.Dir(targetPath) == targetPath {
		return fmt.Errorf("%w: cannot rename onto the filesystem root", ErrInvalidPath)
	}
	if strings.HasPrefix(targetPath, sourcePath+string(filepath.Separator)) {
		return fmt.Errorf("%w: target %q is inside source %q", ErrInvalidPath, target, source)
	}
	return renameError(os.Rename(sourcePath, targetPath), sourcePath, targetPath)
}

// cleanHostFilePath is cleanHostPath for a path that must name a file.
func cleanHostFilePath(hostPath string) (string, error) {
	filePath, err := cleanHostPath(hostPath)
	if err != nil {
		return "", err
	}
	if filepath.Dir(filePath) == filePath {
		return "", ErrIsDirectory
	}
	return filePath, nil
}
