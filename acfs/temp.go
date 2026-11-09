package acfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	kitfs "kit.getarcane.app/fs"
)

const (
	temporaryDirectoryMode = 0o700
	temporaryFileMode      = 0o600
	temporaryNameAttempts  = 10
)

// MkdirTemp creates a uniquely named 0o700 directory in a root-confined directory
// and returns its logical path. The pattern follows os.MkdirTemp.
func (r Root) MkdirTemp(ctx context.Context, logicalDir, pattern string) (string, error) {
	mutation, directory, err := r.openTemporaryDirectory(ctx, logicalDir, pattern, func(resolvedDir string) error {
		return fmt.Errorf("create temporary directory in %q: %w", logicalDir, &fs.PathError{
			Op:   "mkdirat",
			Path: path.Join(resolvedDir, temporaryName(pattern)),
			Err:  syscall.ENOTDIR,
		})
	})
	if err != nil {
		return "", err
	}
	defer func() {
		_ = mutation.Close()
	}()
	defer func() {
		_ = directory.Close()
	}()

	_, candidate, err := createTemporary(pattern, func(name string) (struct{}, error) {
		return struct{}{}, directory.Mkdir(name, temporaryDirectoryMode)
	})
	if err != nil {
		return "", fmt.Errorf("create temporary directory in %q: %w", logicalDir, err)
	}
	return directory.display(candidate), nil
}

// CreateTemp creates a uniquely named 0o600 file in a root-confined directory
// and returns it open with its logical path.
func (r Root) CreateTemp(ctx context.Context, logicalDir, pattern string) (*os.File, string, error) {
	mutation, directory, err := r.openTemporaryDirectory(ctx, logicalDir, pattern, func(resolvedDir string) error {
		return fmt.Errorf("create temporary file in %q: %w", logicalDir, &fs.PathError{
			Op:   "openat",
			Path: path.Join(resolvedDir, temporaryName(pattern)),
			Err:  syscall.ENOTDIR,
		})
	})
	if err != nil {
		return nil, "", err
	}
	defer func() {
		_ = mutation.Close()
	}()
	defer func() {
		_ = directory.Close()
	}()

	file, candidate, err := createTemporary(pattern, func(name string) (*os.File, error) {
		return directory.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, temporaryFileMode)
	})
	if err != nil {
		return nil, "", fmt.Errorf("create temporary file in %q: %w", logicalDir, err)
	}
	return file, directory.display(candidate), nil
}

// openTemporaryDirectory validates pattern and opens logicalDir without following
// symbolic links. notDirectory builds the error for a non-directory.
func (r Root) openTemporaryDirectory(ctx context.Context, logicalDir, pattern string, notDirectory func(resolvedDir string) error) (*mutationRoot, *dir, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := validateTemporaryPattern(pattern); err != nil {
		return nil, nil, err
	}

	relativeDir, err := kitfs.NormalizeLogicalPath(logicalDir)
	if err != nil {
		return nil, nil, err
	}
	err = rejectReservedPath(relativeDir)
	if err != nil {
		return nil, nil, err
	}

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return nil, nil, err
	}
	root, err := mutation.workspace()
	if err != nil {
		_ = mutation.Close()
		return nil, nil, err
	}
	resolvedDir, _, err := resolvePath(root, relativeDir, resolveOptions{FollowFinal: true})
	if err != nil {
		_ = mutation.Close()
		return nil, nil, err
	}
	directory, err := mutation.top.openDir(resolvedDir, dirOptions{})
	if errors.Is(err, syscall.ENOTDIR) {
		err = notDirectory(resolvedDir)
	}
	if err != nil {
		_ = mutation.Close()
		return nil, nil, err
	}
	return mutation, directory, nil
}

// validateTemporaryPattern requires pattern to be a single path component.
func validateTemporaryPattern(pattern string) error {
	if pattern == "" || strings.ContainsRune(pattern, '/') || strings.ContainsRune(pattern, filepath.Separator) ||
		strings.ContainsRune(pattern, '\x00') || filepath.VolumeName(pattern) != "" {
		return fmt.Errorf("%w: temporary pattern must be a single path component", ErrInvalidPath)
	}
	prefix, _, _ := strings.CutLast(pattern, "*")
	return rejectReservedPath(prefix)
}

// createTemporary calls create with random names until one does not exist.
func createTemporary[T any](pattern string, create func(name string) (T, error)) (T, string, error) {
	for range temporaryNameAttempts {
		name := temporaryName(pattern)
		created, err := create(name)
		if err == nil {
			return created, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return *new(T), "", err
		}
	}
	return *new(T), "", errors.New("could not allocate a unique temporary name")
}

// temporaryName returns a random name built from pattern.
func temporaryName(pattern string) string {
	prefix, suffix, _ := strings.CutLast(pattern, "*")
	var randomBytes [8]byte
	_, _ = rand.Read(randomBytes[:])
	return prefix + hex.EncodeToString(randomBytes[:]) + suffix
}

// MkdirTemp creates a uniquely named directory in a host directory like Root.MkdirTemp.
func (Host) MkdirTemp(ctx context.Context, hostDir, pattern string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateTemporaryPattern(pattern); err != nil {
		return "", err
	}
	directory, err := cleanHostPath(hostDir)
	if err != nil {
		return "", err
	}
	_, name, err := createTemporary(pattern, func(name string) (struct{}, error) {
		return struct{}{}, os.Mkdir(filepath.Join(directory, name), temporaryDirectoryMode)
	})
	if err != nil {
		return "", fmt.Errorf("create temporary directory in %q: %w", directory, err)
	}
	return filepath.Join(directory, name), nil
}

// CreateTemp creates a uniquely named file in a host directory like Root.CreateTemp.
func (Host) CreateTemp(ctx context.Context, hostDir, pattern string) (*os.File, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if err := validateTemporaryPattern(pattern); err != nil {
		return nil, "", err
	}
	directory, err := cleanHostPath(hostDir)
	if err != nil {
		return nil, "", err
	}
	file, name, err := createTemporary(pattern, func(name string) (*os.File, error) {
		return os.OpenFile(filepath.Join(directory, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, temporaryFileMode)
	})
	if err != nil {
		return nil, "", fmt.Errorf("create temporary file in %q: %w", directory, err)
	}
	return file, filepath.Join(directory, name), nil
}
