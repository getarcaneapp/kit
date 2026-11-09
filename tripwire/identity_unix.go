//go:build unix

package tripwire

import (
	"os"
	"syscall"
)

type (
	// fileID identifies a directory independent of the path used to reach it.
	fileID struct {
		dev uint64
		ino uint64
	}
)

func identify(path string) (fileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileID{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, os.ErrInvalid
	}
	return fileID{
		dev: uint64(st.Dev), //nolint:unconvert // Dev is int32 on darwin, uint64 on linux.
		ino: st.Ino,
	}, nil
}
