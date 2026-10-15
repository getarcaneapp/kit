package acfs

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	kit "go.getarcane.app/kit/pkg"
	kitfs "go.getarcane.app/kit/pkg/fs"

	acfstypes "go.getarcane.app/acfs/types"
)

func entryFromInfo(root *os.Root, relativePath string, info os.FileInfo) (acfstypes.Entry, error) {
	linkTarget, err := func() (string, error) {
		if info.Mode()&os.ModeSymlink == 0 {
			return "", nil
		}
		target, err := root.Readlink(relativePath)
		if err != nil {
			return "", fmt.Errorf("readlink %q: %w", kitfs.LogicalPath(relativePath), err)
		}
		switch {
		case target == logicalVolumeRoot:
			return "/", nil
		case strings.HasPrefix(target, logicalVolumeRoot+"/"):
			return strings.TrimPrefix(target, logicalVolumeRoot), nil
		case strings.HasPrefix(target, "/"):
			return "(external)", nil
		default:
			return target, nil
		}
	}()
	if err != nil {
		return acfstypes.Entry{}, err
	}

	return acfstypes.Entry{
		ModTime:         info.ModTime().Truncate(time.Second),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		Name:            kit.Ternary(relativePath == ".", "/", path.Base(relativePath)),
		Path:            kitfs.LogicalPath(relativePath),
		Mode:            kitfs.FormatMode(info.Mode()),
		LinkTarget:      linkTarget,
		Size:            info.Size(),
		UnixMode:        uint32(info.Mode()),
		IsDirectory:     info.IsDir(),
		IsSymlink:       info.Mode()&os.ModeSymlink != 0,
	}, nil
}
