// Package utils contains focused filesystem helpers shared by ACFS.
package utils

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrInvalidPath indicates that a logical workspace path is malformed.
var ErrInvalidPath = errors.New("invalid workspace path")

// NormalizeLogicalPath validates an absolute logical workspace path and
// returns the relative form accepted by os.Root.
func NormalizeLogicalPath(logicalPath string) (string, error) {
	if logicalPath == "/" {
		return ".", nil
	}
	if logicalPath == "" || !strings.HasPrefix(logicalPath, "/") || strings.ContainsRune(logicalPath, '\x00') {
		return "", fmt.Errorf("%w: path must be absolute", ErrInvalidPath)
	}

	components := strings.Split(strings.TrimPrefix(logicalPath, "/"), "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("%w: invalid component in %q", ErrInvalidPath, logicalPath)
		}
	}

	return strings.Join(components, "/"), nil
}

// LogicalPath converts an os.Root-relative path to an absolute logical path.
func LogicalPath(relativePath string) string {
	if relativePath == "." || relativePath == "" {
		return "/"
	}
	return "/" + relativePath
}

// FormatMode formats a Go file mode like POSIX stat output.
func FormatMode(mode os.FileMode) string {
	formatted := [10]byte{'-', '-', '-', '-', '-', '-', '-', '-', '-', '-'}

	switch {
	case mode.IsDir():
		formatted[0] = 'd'
	case mode&os.ModeSymlink != 0:
		formatted[0] = 'l'
	case mode&os.ModeNamedPipe != 0:
		formatted[0] = 'p'
	case mode&os.ModeSocket != 0:
		formatted[0] = 's'
	case mode&os.ModeDevice != 0 && mode&os.ModeCharDevice != 0:
		formatted[0] = 'c'
	case mode&os.ModeDevice != 0:
		formatted[0] = 'b'
	case mode&os.ModeIrregular != 0:
		formatted[0] = '?'
	}

	permissions := []struct {
		bit  os.FileMode
		char byte
		pos  int
	}{
		{0o400, 'r', 1},
		{0o200, 'w', 2},
		{0o100, 'x', 3},
		{0o040, 'r', 4},
		{0o020, 'w', 5},
		{0o010, 'x', 6},
		{0o004, 'r', 7},
		{0o002, 'w', 8},
		{0o001, 'x', 9},
	}
	for _, permission := range permissions {
		if mode&permission.bit != 0 {
			formatted[permission.pos] = permission.char
		}
	}

	if mode&os.ModeSetuid != 0 {
		if formatted[3] == 'x' {
			formatted[3] = 's'
		} else {
			formatted[3] = 'S'
		}
	}
	if mode&os.ModeSetgid != 0 {
		if formatted[6] == 'x' {
			formatted[6] = 's'
		} else {
			formatted[6] = 'S'
		}
	}
	if mode&os.ModeSticky != 0 {
		if formatted[9] == 'x' {
			formatted[9] = 't'
		} else {
			formatted[9] = 'T'
		}
	}

	return string(formatted[:])
}
