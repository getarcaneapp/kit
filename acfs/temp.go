package acfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	kitfs "go.getarcane.app/kit/pkg/fs"
)

const (
	temporaryDirectoryMode     = 0o700
	temporaryDirectoryAttempts = 10
)

// MkdirTemp creates a uniquely named directory inside a root-confined
// directory and returns its logical path. The pattern follows os.MkdirTemp:
// the last "*" is replaced by a random string, or one is appended when the
// pattern contains no "*". The pattern must be a single path component and may
// not use the reserved ACFS temporary-write prefix.
//
// The directory is created with mode 0o700 and is not cleaned up
// automatically; callers own its lifetime.
func MkdirTemp(ctx context.Context, rootPath, logicalDir, pattern string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if pattern == "" || strings.ContainsRune(pattern, '/') || strings.ContainsRune(pattern, '\x00') {
		return "", fmt.Errorf("%w: temporary pattern must be a single path component", ErrInvalidPath)
	}
	prefix, suffix, _ := strings.CutLast(pattern, "*")
	if err := rejectReservedPathInternal(prefix); err != nil {
		return "", err
	}

	relativeDir, err := kitfs.NormalizeLogicalPath(logicalDir)
	if err != nil {
		return "", err
	}
	err = rejectReservedPathInternal(relativeDir)
	if err != nil {
		return "", err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = root.Close() }()

	resolvedDir, err := resolvePathInternal(root, relativeDir, true)
	if err != nil {
		return "", err
	}

	var randomBytes [8]byte
	for range temporaryDirectoryAttempts {
		_, err = rand.Read(randomBytes[:])
		if err != nil {
			return "", fmt.Errorf("generate temporary directory name: %w", err)
		}
		candidate := relativePathInternal([]string{resolvedDir, prefix + hex.EncodeToString(randomBytes[:]) + suffix})
		err = root.Mkdir(candidate, temporaryDirectoryMode)
		if err == nil {
			return kitfs.LogicalPath(candidate), nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create temporary directory in %q: %w", logicalDir, err)
		}
	}
	return "", errors.New("could not allocate a unique temporary directory")
}
