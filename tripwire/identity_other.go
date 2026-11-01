//go:build !unix

package tripwire

import "path/filepath"

// fileID identifies a directory by its fully resolved path where the
// platform has no device/inode pair.
type fileID struct {
	path string
}

func identify(path string) (fileID, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fileID{}, err
	}
	abs, err := filepath.Abs(resolved)
	return fileID{path: abs}, err
}
