//go:build unix

package acfs

import (
	"os"
	"syscall"
)

// fileIdentity returns the owner and device and inode numbers of info. Dev's
// type varies by platform, so it is widened through int64.
func fileIdentity(info os.FileInfo) (uid, gid int, device, inode uint64) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, 0
	}
	return int(stat.Uid), int(stat.Gid), uint64(int64(stat.Dev)), stat.Ino
}
