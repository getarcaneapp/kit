// Package atomic provides crash-safe atomic file writes.
//
// WriteFile writes to a temporary file in the destination's directory,
// fsyncs it, applies the requested permissions, and renames it onto the
// destination -- so a reader never observes a partially written file and a
// crash mid-write leaves the previous version intact.
//
// WriteFile's temp-file/fsync/chmod/rename sequence (see atomicFileWriter.Close
// in the source below) and validateDestination's refusal to write through a
// symlink/directory/named pipe/socket/device destination are adapted from
// moby's atomicwriter package:
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
//   - validateDestination does not refuse setuid/setgid/sticky-bit
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
	dir := filepath.Dir(name)

	if err := validateDestination(name); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(name)+".*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", name, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to write temp file %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to sync temp file %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to chmod temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("failed to close temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, name); err != nil {
		cleanup()
		return fmt.Errorf("failed to rename %s to %s: %w", tmpName, name, err)
	}

	return nil
}

// validateDestination refuses to write through a destination that is a
// symlink, directory, named pipe, socket, or device -- adapted from
// moby/sys/atomicwriter's guard of the same name (see package doc comment
// for attribution) against clobbering something other than a plain file
// via the rename.
func validateDestination(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to stat %s: %w", name, err)
	}

	switch {
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

	return nil
}
