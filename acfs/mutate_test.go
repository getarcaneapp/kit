package acfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	if _, err := os.Lstat(filepath.Join(root, "source.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source still present: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "occupied", "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "mover"), 0o750); err != nil {
		t.Fatal(err)
	}
	err = Rename(ctx, root, "/mover", "/occupied")
	if !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Rename onto non-empty directory error = %v, want ErrNotEmpty", err)
	}

	if err := Rename(ctx, root, "/occupied", "/occupied/child/nested"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Rename into own subtree error = %v, want ErrInvalidPath", err)
	}
	if err := Rename(ctx, root, "/", "/anywhere"); !errors.Is(err, ErrRootRemoval) {
		t.Fatalf("Rename of workspace root error = %v, want ErrRootRemoval", err)
	}
}

func TestRemoveSurfacesMissingTargetAndNonEmptyDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()
	if err := os.MkdirAll(filepath.Join(root, "parent", "child"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := Remove(ctx, root, "/parent"); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Remove of non-empty directory error = %v, want ErrNotEmpty", err)
	}
	if err := Remove(ctx, root, "/parent/child"); err != nil {
		t.Fatalf("Remove of empty directory: %v", err)
	}

	// Unlike RemoveAll, Remove reports a missing target so cleanup loops stop.
	if err := Remove(ctx, root, "/parent/child"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Remove of missing target error = %v, want fs.ErrNotExist", err)
	}
	if err := RemoveAll(ctx, root, "/parent/child"); err != nil {
		t.Fatalf("RemoveAll of missing target = %v, want nil", err)
	}
}

func TestMkdirRejectsExistingTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()
	if err := Mkdir(ctx, root, "/created", 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	err := Mkdir(ctx, root, "/created", 0o750)
	if !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Mkdir on existing directory error = %v, want ErrAlreadyExists and fs.ErrExist", err)
	}
}

func TestMkdirTempCreatesUniqueDirectoriesFromPattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()

	first, err := MkdirTemp(ctx, root, "/", "stage-*.tmp")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	second, err := MkdirTemp(ctx, root, "/", "stage-*.tmp")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if first == second {
		t.Fatalf("MkdirTemp returned duplicate path %q", first)
	}

	name := strings.TrimPrefix(first, "/")
	if !strings.HasPrefix(name, "stage-") || !strings.HasSuffix(name, ".tmp") {
		t.Fatalf("temporary name %q does not follow the pattern", name)
	}
	info, err := os.Stat(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != temporaryDirectoryMode {
		t.Fatalf("temporary directory mode = %v, want %v", info.Mode(), os.FileMode(temporaryDirectoryMode))
	}

	if _, err := MkdirTemp(ctx, root, "/", "nested/pattern"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("MkdirTemp with multi-component pattern error = %v, want ErrInvalidPath", err)
	}
}

func TestStatFollowingSymlinkResolvesToItsTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "nested", "compose.yaml"), "services: {}", 0o640)
	if err := os.Symlink("nested/compose.yaml", filepath.Join(root, "link.yaml")); err != nil {
		t.Fatal(err)
	}

	linked, err := Stat(ctx, root, "/link.yaml", false)
	if err != nil {
		t.Fatal(err)
	}
	if !linked.IsSymlink {
		t.Fatalf("Stat reported IsSymlink = false for a symlink")
	}

	followed, err := Stat(ctx, root, "/link.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	if followed.IsSymlink {
		t.Fatalf("following Stat reported IsSymlink = true")
	}
	if followed.Path != "/nested/compose.yaml" {
		t.Fatalf("following Stat path = %q, want %q", followed.Path, "/nested/compose.yaml")
	}
	if followed.Size != int64(len("services: {}")) {
		t.Fatalf("following Stat size = %d, want %d", followed.Size, len("services: {}"))
	}
	if os.FileMode(followed.UnixMode).Perm() != 0o640 {
		t.Fatalf("following Stat UnixMode = %v, want 0640", os.FileMode(followed.UnixMode))
	}
}

func TestExistsAndLogicalPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()
	writeFixtureFile(t, filepath.Join(root, "present.txt"), "x", 0o640)

	present, err := Exists(ctx, root, "/present.txt")
	if err != nil || !present {
		t.Fatalf("Exists(present) = %v, %v; want true, nil", present, err)
	}
	missing, err := Exists(ctx, root, "/missing/deep.txt")
	if err != nil || missing {
		t.Fatalf("Exists(missing) = %v, %v; want false, nil", missing, err)
	}

	logical, err := LogicalPath(root, filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if logical != "/nested/file.txt" {
		t.Fatalf("LogicalPath = %q, want %q", logical, "/nested/file.txt")
	}
	if _, err := LogicalPath(root, filepath.Dir(root)); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("LogicalPath escape error = %v, want ErrOutsideRoot", err)
	}
}
