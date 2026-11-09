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

	if err := (Root{Path: root}).Rename(ctx, "/source.txt", "/target.txt"); err != nil {
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
	err = Root{Path: root}.Rename(ctx, "/mover", "/occupied")
	if !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Rename onto non-empty directory error = %v, want ErrNotEmpty", err)
	}

	err = Root{Path: root}.Rename(ctx, "/occupied", "/occupied/child/nested")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Rename into own subtree error = %v, want ErrInvalidPath", err)
	}
	err = Root{Path: root}.Rename(ctx, "/", "/anywhere")
	if !errors.Is(err, ErrRootRemoval) {
		t.Fatalf("Rename of workspace root error = %v, want ErrRootRemoval", err)
	}
}

func BenchmarkRename(b *testing.B) {
	root := Root{Path: b.TempDir()}
	writeFixtureFile(b, filepath.Join(root.Path, "a"), "x", 0o600)
	names := [2]string{"/a", "/b"}
	b.ReportAllocs()
	iteration := 0
	for b.Loop() {
		if err := root.Rename(b.Context(), names[iteration%2], names[(iteration+1)%2]); err != nil {
			b.Fatal(err)
		}
		iteration++
	}
}
