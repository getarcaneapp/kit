//go:build unix

package acfs

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	acfstypes "kit.getarcane.app/acfs/types"
)

func TestCopyDirSkipsSymlinksAndRecordsUncopyableEntries(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	destination := t.TempDir()
	ctx := t.Context()

	if err := os.Mkdir(filepath.Join(source, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(source, "nested", "compose.yaml"), "services: {}", 0o640)
	if err := os.Symlink("nested/compose.yaml", filepath.Join(source, "link.yaml")); err != nil {
		t.Fatal(err)
	}
	// A socket or FIFO left behind by a running container has no copyable
	// content: reading it fails with ENXIO, not a permission error (#3003).
	if err := syscall.Mkfifo(filepath.Join(source, "runtime.sock"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Root{Path: source}.CopyTo(ctx, Root{Path: destination}, acfstypes.CopyOptions{TolerateUnreadable: true})
	if err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	if result.Copied != 1 {
		t.Fatalf("CopyDir copied = %d, want 1", result.Copied)
	}
	if !slices.Contains(result.Skipped, "runtime.sock") {
		t.Fatalf("CopyDir skipped = %v, want it to contain runtime.sock", result.Skipped)
	}

	contents, err := os.ReadFile(filepath.Join(destination, "nested", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "services: {}" {
		t.Fatalf("copied contents = %q", contents)
	}
	_, err = os.Lstat(filepath.Join(destination, "link.yaml"))
	if err == nil {
		t.Fatal("CopyDir recreated a symbolic link in the destination")
	}
}

func TestCopyToRefusesNonRegularDestination(t *testing.T) {
	t.Parallel()

	source, destination := t.TempDir(), t.TempDir()
	writeFixtureFile(t, filepath.Join(source, "file"), "source", 0o644)
	if err := syscall.Mkfifo(filepath.Join(destination, "file"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Root{Path: source}.CopyTo(t.Context(), Root{Path: destination}, acfstypes.CopyOptions{})
	if !errors.Is(err, ErrNotFile) {
		t.Fatalf("CopyTo onto fifo = %v, want ErrNotFile", err)
	}
}
