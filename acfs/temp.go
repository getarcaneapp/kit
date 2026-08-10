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

	"go.getarcane.app/acfs/pkg/utils"
)

const (
	temporaryDirectoryMode     = 0o700
	temporaryDirectoryAttempts = 10
)

// temporaryNamePartsInternal splits an os.MkdirTemp-style pattern around its
// last "*", which is where the random component is substituted.
func temporaryNamePartsInternal(pattern string) (prefix, suffix string) {
	if index := strings.LastIndexByte(pattern, '*'); index >= 0 {
		return pattern[:index], pattern[index+1:]
	}
	return pattern, ""
}

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
	prefix, suffix := temporaryNamePartsInternal(pattern)
	if err := rejectReservedPathInternal(prefix); err != nil {
		return "", err
	}

	relativeDir, err := utils.NormalizeLogicalPath(logicalDir)
	if err != nil {
		return "", err
	}
	if err := rejectReservedPathInternal(relativeDir); err != nil {
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
		if _, err := rand.Read(randomBytes[:]); err != nil {
			return "", fmt.Errorf("generate temporary directory name: %w", err)
		}
		candidate := relativePathInternal([]string{resolvedDir, prefix + hex.EncodeToString(randomBytes[:]) + suffix})
		err := root.Mkdir(candidate, temporaryDirectoryMode)
		if err == nil {
			return utils.LogicalPath(candidate), nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create temporary directory in %q: %w", logicalDir, err)
		}
	}
	return "", errors.New("could not allocate a unique temporary directory")
}
