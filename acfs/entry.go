package acfs

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	acfstypes "go.getarcane.app/acfs/types"
	kitfs "go.getarcane.app/kit/pkg/fs"
)

func entryFromInfoInternal(root *os.Root, relativePath string, info os.FileInfo) (acfstypes.Entry, error) {
	logicalPath := kitfs.LogicalPath(relativePath)
	name := path.Base(relativePath)
	if relativePath == "." {
		name = "/"
	}

	entry := acfstypes.Entry{
		Name:            name,
		Path:            logicalPath,
		Mode:            kitfs.FormatMode(info.Mode()),
		UnixMode:        uint32(info.Mode()),
		Size:            info.Size(),
		ModTime:         info.ModTime().Truncate(time.Second),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		IsDirectory:     info.IsDir(),
		IsSymlink:       info.Mode()&os.ModeSymlink != 0,
	}

	if !entry.IsSymlink {
		return entry, nil
	}

	target, err := root.Readlink(relativePath)
	if err != nil {
		return acfstypes.Entry{}, fmt.Errorf("readlink %q: %w", logicalPath, err)
	}

	switch {
	case target == logicalVolumeRoot:
		entry.LinkTarget = "/"
	case strings.HasPrefix(target, logicalVolumeRoot+"/"):
		entry.LinkTarget = strings.TrimPrefix(target, logicalVolumeRoot)
	case strings.HasPrefix(target, "/"):
		entry.LinkTarget = "(external)"
	default:
		entry.LinkTarget = target
	}

	return entry, nil
}
