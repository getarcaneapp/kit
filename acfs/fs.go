package acfs

import (
	"context"
	"io"
	"os"
	"time"

	acfstypes "kit.getarcane.app/acfs/types"
)

type (
	// FS is the operation set shared by Root and Host. Copy, mirror, and Apply are
	// Root-only; Host has CopyDir and MirrorDir.
	FS interface {
		List(ctx context.Context, path string) ([]acfstypes.Entry, error)
		ListEach(ctx context.Context, path string, visit func(acfstypes.Entry) error) error
		Stat(ctx context.Context, path string, options acfstypes.StatOptions) (acfstypes.Entry, error)
		Exists(ctx context.Context, path string) (bool, error)

		OpenRead(ctx context.Context, path string, maxBytes int64) (io.ReadCloser, int64, error)
		OpenReadSeek(ctx context.Context, path string) (io.ReadSeekCloser, int64, error)
		ReadFile(ctx context.Context, path string) ([]byte, error)
		ReadTo(ctx context.Context, path string, destination io.Writer, maxBytes int64) (int64, error)
		OpenFile(ctx context.Context, path string, options OpenOptions) (*os.File, error)
		CreateTemp(ctx context.Context, dir, pattern string) (*os.File, string, error)

		Write(ctx context.Context, path string, data []byte, options WriteOptions) error
		WriteFrom(ctx context.Context, path string, source io.Reader, options WriteFromOptions) (int64, error)
		WriteAt(ctx context.Context, path string, offset int64, data []byte) error
		Sync(ctx context.Context, path string) error

		Mkdir(ctx context.Context, path string, mode os.FileMode) error
		MkdirAll(ctx context.Context, path string, mode os.FileMode) error
		MkdirTemp(ctx context.Context, dir, pattern string) (string, error)
		Rename(ctx context.Context, source, target string) error
		Remove(ctx context.Context, path string) error
		RemoveAll(ctx context.Context, path string) error

		Chmod(ctx context.Context, path string, mode os.FileMode) error
		Chown(ctx context.Context, path string, uid, gid int) error
		Lchown(ctx context.Context, path string, uid, gid int) error
		Chtimes(ctx context.Context, path string, atime, mtime time.Time) error
		Symlink(ctx context.Context, target, linkPath string) error
		Readlink(ctx context.Context, path string) (string, error)
	}
)

var (
	_ FS = Root{}
	_ FS = Host{}
)
