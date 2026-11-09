//go:build !unix

package acfs

import "os"

// fileIdentity reports zeros where the platform has no Stat_t.
func fileIdentity(os.FileInfo) (uid, gid int, device, inode uint64) {
	return 0, 0, 0, 0
}
