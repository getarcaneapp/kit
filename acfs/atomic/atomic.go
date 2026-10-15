// Package atomic provides crash-safe atomic file writes at an absolute path.
//
// It is the unrooted counterpart to the root package: acfs.Write confines
// every write to a workspace root and replaces a symlinked destination, while
// atomic.WriteFile takes any absolute path and refuses a symlinked destination.
// The two symlink policies live in separate namespaces on purpose — reach for
// this package only when there is no natural root to confine the write to.
//
// WriteFile writes to a temporary file in the destination's directory,
// fsyncs it, applies the requested permissions, and renames it onto the
// destination -- so a reader never observes a partially written file and a
// crash mid-write leaves the previous version intact.
//
// WriteFile's temp-file/fsync/chmod/rename sequence and its destination check's
// refusal to write through a symlink/directory/named pipe/socket/device
// destination are adapted from moby's atomicwriter package:
//
//	Source:  github.com/moby/sys/atomicwriter
//	License: Apache License 2.0 (https://github.com/moby/sys/blob/main/LICENSE)
//	File:    https://github.com/moby/sys/blob/main/atomicwriter/atomicwriter.go
//
// This is a reimplementation against only the standard library, not a copy,
// so this module stays dependency-free. It deliberately simplifies moby's
// version in a few ways:
//   - os.CreateTemp is used instead of moby's sequential.CreateTemp, a
//     Windows standby-list optimization that isn't a correctness
//     requirement here.
//   - WriteFile's destination check does not refuse setuid/setgid/sticky-bit
//     destinations, and does not pre-check that the parent directory
//     exists (every caller here already MkdirAll's its target directory).
//   - Parent-directory fsync is not performed, matching moby's own
//     behavior: atomic rename already guarantees a reader never sees a
//     torn file, and the extra fsync only narrows the
//     crash-immediately-after-rename window.
package atomic

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile atomically writes data to name with the given permissions. It
// is a drop-in, crash-safe replacement for os.WriteFile: on success the
// destination either has its previous content or the new content in full,
// never a partial write.
func WriteFile(name string, data []byte, perm os.FileMode) error {
	// WriteFile's destination check refuses to write through a destination that is a
	// symlink, directory, named pipe, socket, or device -- adapted from
	// moby/sys/atomicwriter's validateDestination guard (see package doc comment
	// for attribution) against clobbering something other than a plain file
	// via the rename.
	info, err := os.Lstat(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("failed to stat %s: %w", name, err)
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("refusing to write to %s: destination is a symlink", name)
	case info.IsDir():
		return fmt.Errorf("refusing to write to %s: destination is a directory", name)
	case info.Mode()&os.ModeNamedPipe != 0:
		return fmt.Errorf("refusing to write to %s: destination is a named pipe", name)
	case info.Mode()&os.ModeSocket != 0:
		return fmt.Errorf("refusing to write to %s: destination is a socket", name)
	case info.Mode()&os.ModeDevice != 0:
		return fmt.Errorf("refusing to write to %s: destination is a device", name)
	}

	tmp, err := os.CreateTemp(filepath.Dir(name), ".tmp-"+filepath.Base(name)+".*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", name, err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}

	_, err = tmp.Write(data)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to write temp file %s: %w", tmp.Name(), err)
	}
	err = tmp.Sync()
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to sync temp file %s: %w", tmp.Name(), err)
	}
	err = tmp.Chmod(perm)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to chmod temp file %s: %w", tmp.Name(), err)
	}
	err = tmp.Close()
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to close temp file %s: %w", tmp.Name(), err)
	}
	err = os.Rename(tmp.Name(), name)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to rename %s to %s: %w", tmp.Name(), name, err)
	}

	return nil
}
