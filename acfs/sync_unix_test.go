//go:build unix

package acfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSyncSpecialFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, "file"), "data", 0o600)
	if err := os.Symlink("file", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (Root{Path: dir}).Sync(t.Context(), "/link"); !errors.Is(err, ErrSymlink) {
		t.Errorf("Root.Sync(link) = %v, want ErrSymlink", err)
	}
	if err := (Host{}).Sync(t.Context(), filepath.Join(dir, "link")); err != nil {
		t.Errorf("Host.Sync(link): %v", err)
	}
	if err := (Root{Path: dir}).Sync(t.Context(), "/fifo"); !errors.Is(err, ErrNotFile) {
		t.Errorf("Root.Sync(fifo) = %v, want ErrNotFile", err)
	}
	if err := (Host{}).Sync(t.Context(), filepath.Join(dir, "fifo")); !errors.Is(err, ErrNotFile) {
		t.Errorf("Host.Sync(fifo) = %v, want ErrNotFile", err)
	}
}
