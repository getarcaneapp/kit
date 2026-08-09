package acfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"go.getarcane.app/acfs/pkg/utils"
)

type contextReadCloserInternal struct {
	ctx    context.Context
	reader io.Reader
	closer io.Closer
}

func (r *contextReadCloserInternal) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func (r *contextReadCloserInternal) Close() error {
	return r.closer.Close()
}

// OpenRead opens a file for a root-confined streaming read. The returned size
// is the number of bytes available after applying maxBytes; a non-positive
// limit reads the complete file.
func OpenRead(ctx context.Context, rootPath, logicalPath string, maxBytes int64) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}

	relativePath, err := utils.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return nil, 0, err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedPath, err := resolvePathInternal(root, relativePath, true)
	if err != nil {
		return nil, 0, err
	}

	file, err := root.Open(resolvedPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open %q: %w", utils.LogicalPath(resolvedPath), err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat %q: %w", utils.LogicalPath(resolvedPath), err)
	}
	if info.IsDir() {
		_ = file.Close()
		return nil, 0, ErrIsDirectory
	}

	size := info.Size()
	var reader io.Reader = file
	if maxBytes > 0 && maxBytes < size {
		size = maxBytes
		reader = io.LimitReader(file, maxBytes)
	}

	return &contextReadCloserInternal{ctx: ctx, reader: reader, closer: file}, size, nil
}

// ReadTo streams a root-confined file into destination. A non-positive limit
// reads the complete file.
func ReadTo(ctx context.Context, rootPath, logicalPath string, destination io.Writer, maxBytes int64) (int64, error) {
	if destination == nil {
		return 0, errors.New("destination writer is required")
	}
	reader, _, err := OpenRead(ctx, rootPath, logicalPath, maxBytes)
	if err != nil {
		return 0, err
	}
	defer func() { _ = reader.Close() }()

	written, err := io.Copy(destination, reader)
	if err != nil {
		return written, fmt.Errorf("read %q: %w", logicalPath, err)
	}
	return written, nil
}
