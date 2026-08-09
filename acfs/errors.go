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
)
