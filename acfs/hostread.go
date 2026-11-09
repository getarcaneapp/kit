package acfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"kit.getarcane.app"
)

// OpenRead opens a host file for a streaming read of at most maxBytes when
// positive. A special file has no known size: it reports -1 and is read to EOF
// or maxBytes.
func (h Host) OpenRead(ctx context.Context, hostPath string, maxBytes int64) (io.ReadCloser, int64, error) {
	file, info, err := h.openRead(ctx, hostPath)
	if err != nil {
		return nil, 0, err
	}
	if info.Mode().IsRegular() {
		size := kit.Ternary(maxBytes > 0, min(maxBytes, info.Size()), info.Size())
		return &contextReadCloser{
			contextReader: contextReader{ctx: ctx, reader: io.LimitReader(file, size)},
			Closer:        file,
		}, size, nil
	}
	reader := io.Reader(file)
	if maxBytes > 0 {
		reader = io.LimitReader(file, maxBytes)
	}
	return &contextReadCloser{
		contextReader: contextReader{ctx: ctx, reader: reader},
		Closer:        file,
	}, -1, nil
}

// OpenReadSeek opens a host regular file for a seekable read.
func (Host) OpenReadSeek(ctx context.Context, hostPath string) (io.ReadSeekCloser, int64, error) {
	file, info, err := Host{}.openRead(ctx, hostPath)
	if err != nil {
		return nil, 0, err
	}
	return &contextReadSeekCloser{ctx: ctx, file: file}, info.Size(), nil
}

// ReadFile reads a host file to EOF, so /proc files with size zero and special
// files are read.
func (h Host) ReadFile(ctx context.Context, hostPath string) ([]byte, error) {
	file, info, err := h.openRead(ctx, hostPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	reader := contextReader{ctx: ctx, reader: file}
	content := make([]byte, 0, max(info.Size()+1, 512))
	for {
		read, readErr := reader.Read(content[len(content):cap(content)])
		content = content[:len(content)+read]
		if errors.Is(readErr, io.EOF) {
			readErr = ctx.Err()
			if readErr == nil {
				return content, nil
			}
		}
		if readErr != nil {
			return nil, fmt.Errorf("read %q: %w", hostPath, readErr)
		}
		if len(content) == cap(content) {
			content = append(content, 0)[:len(content)]
		}
	}
}

// ReadTo streams a host file into destination, at most maxBytes when positive.
func (h Host) ReadTo(ctx context.Context, hostPath string, destination io.Writer, maxBytes int64) (int64, error) {
	if destination == nil {
		return 0, errors.New("destination writer is required")
	}
	file, _, err := h.openRead(ctx, hostPath)
	if err != nil {
		return 0, err
	}
	reader := io.Reader(file)
	if maxBytes > 0 {
		reader = io.LimitReader(file, maxBytes)
	}
	defer func() {
		_ = file.Close()
	}()
	written, err := io.Copy(destination, contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return written, fmt.Errorf("read %q: %w", hostPath, err)
	}
	return written, nil
}

// openRead opens a host file for reading, checking the type before opening so
// a FIFO is opened only when h.Special allows it.
func (h Host) openRead(ctx context.Context, hostPath string) (*os.File, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	filePath, err := cleanHostPath(hostPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat %q: %w", filePath, err)
	}
	err = requireFile(info, h.Special)
	if err != nil {
		return nil, nil, err
	}

	file, err := os.OpenFile(filePath, os.O_RDONLY|kit.Ternary(info.Mode().IsRegular(), syscall.O_NONBLOCK, 0), 0)
	if err != nil {
		return nil, nil, openError(err, filePath)
	}
	info, err = file.Stat()
	if err == nil {
		err = requireFile(info, h.Special)
	}
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

// requireFile reports ErrIsDirectory for a directory and, unless special
// files are allowed, ErrNotFile for any other non-regular file.
func requireFile(info os.FileInfo, special bool) error {
	switch {
	case info.IsDir():
		return ErrIsDirectory
	case !special && !info.Mode().IsRegular():
		return ErrNotFile
	}
	return nil
}
