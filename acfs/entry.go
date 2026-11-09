package acfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"kit.getarcane.app"
	kitfs "kit.getarcane.app/fs"

	acfstypes "kit.getarcane.app/acfs/types"
)

func newEntry(name, logicalPath, mode, linkTarget string, info os.FileInfo) acfstypes.Entry {
	return acfstypes.Entry{
		ModTime:         info.ModTime().Truncate(time.Second),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		Name:            name,
		Path:            logicalPath,
		Mode:            mode,
		LinkTarget:      linkTarget,
		Size:            info.Size(),
		UnixMode:        uint32(info.Mode()),
		IsDirectory:     info.IsDir(),
		IsSymlink:       info.Mode()&os.ModeSymlink != 0,
	}
}

// entryLinkTarget reads the symbolic link name in directory, which is
// relativePath from the root. Host targets are returned verbatim.
func entryLinkTarget(directory *os.Root, name, relativePath string, info os.FileInfo, host bool) (string, error) {
	if info.Mode()&os.ModeSymlink == 0 {
		return "", nil
	}
	target, err := directory.Readlink(name)
	if err != nil {
		if name != relativePath {
			err = rootPathError("readlinkat", relativePath, err)
		}
		return "", fmt.Errorf("readlink %q: %w", kit.Ternary(host, relativePath, kitfs.LogicalPath(relativePath)), err)
	}
	switch {
	case host:
		return target, nil
	case target == logicalVolumeRoot:
		return "/", nil
	case strings.HasPrefix(target, logicalVolumeRoot+"/"):
		return strings.TrimPrefix(target, logicalVolumeRoot), nil
	case strings.HasPrefix(target, "/"):
		return "(external)", nil
	default:
		return target, nil
	}
}

// visitChildren visits the Entry of each child of directory, hiding reserved
// temporary names. Each child is stat'ed afresh, so changes made by visit are seen.
func visitChildren(ctx context.Context, directory *os.Root, names []string, pathPrefix string, host bool, visit func(acfstypes.Entry) error) error {
	var modeText string
	var lastMode os.FileMode
	for _, name := range names {
		if strings.HasPrefix(name, temporaryWritePrefix) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		logicalPath := pathPrefix + name
		relativePath := kit.Ternary(host, logicalPath, logicalPath[1:])
		info, err := directory.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat %q: %w", logicalPath, rootPathError("statat", relativePath, err))
		}
		linkTarget, err := entryLinkTarget(directory, name, relativePath, info, host)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if modeText == "" || info.Mode() != lastMode {
			lastMode, modeText = info.Mode(), kitfs.FormatMode(info.Mode())
		}

		err = visit(newEntry(name, logicalPath, modeText, linkTarget, info))
		if err != nil {
			return fmt.Errorf("visit %q: %w", logicalPath, err)
		}
	}
	return nil
}

// rootPathError rewrites an error from a call relative to an opened directory
// to report rootPath.
func rootPathError(op, rootPath string, err error) error {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return &fs.PathError{Op: op, Path: rootPath, Err: pathErr.Err}
	}
	return &fs.PathError{Op: op, Path: rootPath, Err: err}
}
