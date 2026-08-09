package acfs

import (
	"errors"

	"go.getarcane.app/acfs/pkg/utils"
)

var (
	// ErrInvalidPath indicates that a logical workspace path is malformed.
	ErrInvalidPath = utils.ErrInvalidPath
	// ErrOutsideRoot indicates that a path or symlink escapes the workspace root.
	ErrOutsideRoot = errors.New("path escapes workspace root")
	// ErrSymlinkLoop indicates that symlink resolution exceeded its safe limit.
	ErrSymlinkLoop = errors.New("too many symbolic links")
	// ErrIsDirectory indicates that a file operation targeted a directory.
	ErrIsDirectory = errors.New("path is a directory")
	// ErrSizeMismatch indicates that a write did not contain its declared size.
	ErrSizeMismatch = errors.New("input size does not match declared size")
	// ErrRootRemoval indicates an attempt to remove the workspace root.
	ErrRootRemoval = errors.New("cannot remove workspace root")
	// ErrSymlink indicates that a mutation path contains a symbolic link.
	ErrSymlink = errors.New("symbolic links are not allowed for mutations")
	// ErrAlreadyExists indicates that a create or move destination exists.
	ErrAlreadyExists = errors.New("destination already exists")
	// ErrNotDirectory indicates that a required directory is not a directory.
	ErrNotDirectory = errors.New("path is not a directory")
	// ErrNotFile indicates that a required regular file is not a regular file.
	ErrNotFile = errors.New("path is not a regular file")
	// ErrNotEmpty indicates that a non-recursive directory removal was requested.
	ErrNotEmpty = errors.New("directory is not empty")
)
