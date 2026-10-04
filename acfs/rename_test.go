package acfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameReplacesFileAndRejectsNonEmptyDirectoryTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()
	writeFixtureFile(t, filepath.Join(root, "source.txt"), "source", 0o640)
	writeFixtureFile(t, filepath.Join(root, "target.txt"), "target", 0o640)

	if err := Rename(ctx, root, "/source.txt", "/target.txt"); err != nil {
		t.Fatalf("Rename onto existing file: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "source" {
		t.Fatalf("target contents = %q, want %q", contents, "source")
	}
	_, err = os.Lstat(filepath.Join(root, "source.txt"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source still present: %v", err)
	}

	err = os.MkdirAll(filepath.Join(root, "occupied", "child"), 0o750)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(filepath.Join(root, "mover"), 0o750)
	if err != nil {
		t.Fatal(err)
	}
	err = Rename(ctx, root, "/mover", "/occupied")
	if !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Rename onto non-empty directory error = %v, want ErrNotEmpty", err)
	}

	err = Rename(ctx, root, "/occupied", "/occupied/child/nested")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Rename into own subtree error = %v, want ErrInvalidPath", err)
	}
	err = Rename(ctx, root, "/", "/anywhere")
	if !errors.Is(err, ErrRootRemoval) {
		t.Fatalf("Rename of workspace root error = %v, want ErrRootRemoval", err)
	}
}
