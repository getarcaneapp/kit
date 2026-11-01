package atomic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_ReplacesContentAndLeavesNoTempResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")

	if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
		t.Fatalf("failed to seed file: %v", err)
	}

	if err := WriteFile(path, []byte("new content"), 0o640); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(got) != "new content" {
		t.Fatalf("content = %q, want %q", string(got), "new content")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want %v", info.Mode().Perm(), os.FileMode(0o640))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("found leftover temp file: %s", entry.Name())
		}
	}
}

func TestWriteFile_RefusesSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(target, []byte("real content"), 0o600); err != nil {
		t.Fatalf("failed to seed target file: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	if err := WriteFile(link, []byte("clobbered"), 0o640); err == nil {
		t.Fatal("WriteFile returned nil error, want non-nil for symlink destination")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if string(got) != "real content" {
		t.Fatalf("target content = %q, want %q", string(got), "real content")
	}
}
