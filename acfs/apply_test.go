package acfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	acfstypes "go.getarcane.app/acfs/types"
)

func TestApplyPerformsOrderedMutations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, "create"), []byte("created"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "update"), []byte("updated"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{
			{Operation: acfstypes.ApplyCreateFolder, Path: "/config"},
			{Operation: acfstypes.ApplyCreateFile, Path: "/config/file", StagedName: "create", Size: 7},
			{Operation: acfstypes.ApplyUpdateFile, Path: "/config/file", StagedName: "update", Size: 7},
			{Operation: acfstypes.ApplyRename, Path: "/config/file", TargetPath: "/config/renamed"},
			{Operation: acfstypes.ApplyCreateFolder, Path: "/archive"},
			{Operation: acfstypes.ApplyMove, Path: "/config/renamed", TargetPath: "/archive/renamed"},
		},
	}
	applied, err := Apply(context.Background(), root, staging, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(manifest.Changes) {
		t.Fatalf("applied = %d, want %d", applied, len(manifest.Changes))
	}
	content, err := os.ReadFile(filepath.Join(root, "archive", "renamed"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "updated" {
		t.Fatalf("content = %q, want updated", content)
	}
}

func TestApplyRejectsSymlinksAndNonRecursiveDirectoryRemoval(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staging := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "nested", "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	_, err := Apply(context.Background(), root, staging, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{Operation: acfstypes.ApplyDelete, Path: "/link/nested", Recursive: true}},
	})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink mutation error = %v, want ErrSymlink", err)
	}

	_, err = Apply(context.Background(), root, staging, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{Operation: acfstypes.ApplyDelete, Path: "/real", Recursive: false}},
	})
	if !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("non-recursive delete error = %v, want ErrNotEmpty", err)
	}

	applied, err := Apply(context.Background(), root, staging, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{Operation: acfstypes.ApplyDelete, Path: "/real", Recursive: true}},
	})
	if err != nil || applied != 1 {
		t.Fatalf("recursive delete = (%d, %v)", applied, err)
	}
}

func TestApplyReportsFailedChangeIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staging := t.TempDir()
	_, err := Apply(context.Background(), root, staging, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{
			{Operation: acfstypes.ApplyCreateFolder, Path: "/created"},
			{Operation: acfstypes.ApplyCreateFolder, Path: "/created"},
		},
	})
	var applyErr *acfstypes.ApplyError
	if !errors.As(err, &applyErr) || applyErr.Index != 1 || !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("apply error = %#v, want change 1 collision", err)
	}
}

func TestApplyPreservesUpdateModeAndRejectsOverlappingRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staging := t.TempDir()
	destination := filepath.Join(root, "file")
	if err := os.WriteFile(destination, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "update"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(context.Background(), root, staging, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{
			Operation:  acfstypes.ApplyUpdateFile,
			Path:       "/file",
			StagedName: "update",
			Size:       3,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("updated mode = %o, want 750", info.Mode().Perm())
	}

	overlapping := filepath.Join(root, "staging")
	if err := os.Mkdir(overlapping, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(context.Background(), root, overlapping, acfstypes.ApplyManifest{Version: acfstypes.ProtocolVersion})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("overlapping roots error = %v, want ErrInvalidPath", err)
	}
}
