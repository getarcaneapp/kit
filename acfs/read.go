package acfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"kit.getarcane.app"
	kitfs "kit.getarcane.app/fs"
)

type contextReadCloser struct {
	contextReader
	io.Closer
}

// OpenRead opens a root-confined file for a streaming read of at most maxBytes
// when positive.
func (r Root) OpenRead(ctx context.Context, logicalPath string, maxBytes int64) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return nil, 0, err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return nil, 0, err
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return nil, 0, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()

	resolvedPath, info, err := resolvePath(root, relativePath, resolveOptions{FollowFinal: true})
	if err != nil {
		return nil, 0, err
	}
	if info == nil {
		info, err = root.Stat(resolvedPath)
		if err != nil {
			return nil, 0, fmt.Errorf("stat %q: %w", kitfs.LogicalPath(resolvedPath), err)
		}
	}
	if info.IsDir() {
		return nil, 0, ErrIsDirectory
	}
	if !info.Mode().IsRegular() {
		return nil, 0, ErrNotFile
	}

	file, err := root.OpenFile(resolvedPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open %q: %w", kitfs.LogicalPath(resolvedPath), err)
	}
	info, err = file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat %q: %w", kitfs.LogicalPath(resolvedPath), err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, ErrNotFile
	}

	size := kit.Ternary(maxBytes > 0, min(maxBytes, info.Size()), info.Size())
	return &contextReadCloser{
		ctx:    ctx,
		reader: io.LimitReader(file, size),
		Closer: file,
	}, size, nil
}

type contextReadSeekCloser struct {
	ctx  context.Context
	file *os.File
}

func (r *contextReadSeekCloser) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(buffer)
}

func (r *contextReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Seek(offset, whence)
}

func (r *contextReadSeekCloser) Close() error {
	return r.file.Close()
}

// OpenReadSeek opens a root-confined regular file for a seekable read.
func (r Root) OpenReadSeek(ctx context.Context, logicalPath string) (io.ReadSeekCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return nil, 0, err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return nil, 0, err
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return nil, 0, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()

	resolvedPath, _, err := resolvePath(root, relativePath, resolveOptions{FollowFinal: true})
	if err != nil {
		return nil, 0, err
	}

	file, err := root.OpenFile(resolvedPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open %q: %w", kitfs.LogicalPath(resolvedPath), err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat %q: %w", kitfs.LogicalPath(resolvedPath), err)
	}
	if info.IsDir() {
		_ = file.Close()
		return nil, 0, ErrIsDirectory
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, ErrNotFile
	}

	return &contextReadSeekCloser{ctx: ctx, file: file}, info.Size(), nil
}

// ReadFile returns the contents of a root-confined regular file.
func (r Root) ReadFile(ctx context.Context, logicalPath string) ([]byte, error) {
	reader, size, err := r.OpenRead(ctx, logicalPath, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = reader.Close()
	}()

	// A file that shrank since open yields a short read, like reaching EOF.
	content := make([]byte, size)
	read, err := io.ReadFull(reader, content)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", logicalPath, err)
	}
	return content[:read], nil
}

// ReadTo streams a root-confined file into destination, at most maxBytes when
// positive.
func (r Root) ReadTo(ctx context.Context, logicalPath string, destination io.Writer, maxBytes int64) (int64, error) {
	if destination == nil {
		return 0, errors.New("destination writer is required")
	}
	reader, _, err := r.OpenRead(ctx, logicalPath, maxBytes)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = reader.Close()
	}()
	written, err := io.Copy(destination, reader)
	if err != nil {
		return written, fmt.Errorf("read %q: %w", logicalPath, err)
	}
	return written, nil
}
