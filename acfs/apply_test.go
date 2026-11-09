package acfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	acfstypes "kit.getarcane.app/acfs/types"
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
	applied, err := Root{Path: root}.Apply(t.Context(), Root{Path: staging}, manifest)
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

	_, err := Root{Path: root}.Apply(t.Context(), Root{Path: staging}, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{Operation: acfstypes.ApplyDelete, Path: "/link/nested", Recursive: true}},
	})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink mutation error = %v, want ErrSymlink", err)
	}

	_, err = Root{Path: root}.Apply(t.Context(), Root{Path: staging}, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{Operation: acfstypes.ApplyDelete, Path: "/real", Recursive: false}},
	})
	if !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("non-recursive delete error = %v, want ErrNotEmpty", err)
	}

	applied, err := Root{Path: root}.Apply(t.Context(), Root{Path: staging}, acfstypes.ApplyManifest{
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
	_, err := Root{Path: root}.Apply(t.Context(), Root{Path: staging}, acfstypes.ApplyManifest{
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
	_, err := Root{Path: root}.Apply(t.Context(), Root{Path: staging}, acfstypes.ApplyManifest{
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
	err = os.Mkdir(overlapping, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Root{Path: root}.Apply(t.Context(), Root{Path: overlapping}, acfstypes.ApplyManifest{Version: acfstypes.ProtocolVersion})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("overlapping roots error = %v, want ErrInvalidPath", err)
	}
}

func TestApplyCreateRejectsReplacedPublication(t *testing.T) {
	tests := map[string]func(root, victim string) (func(string), func()){
		"temporary link": func(root, victim string) (func(string), func()) {
			return func(temporaryName string) {
				replaceWithLink(t, filepath.Join(root, temporaryName), victim)
			}, nil
		},
		"destination": func(root, victim string) (func(string), func()) {
			return nil, func() {
				replaceWithLink(t, filepath.Join(root, "file"), victim)
			}
		},
	}
	for name, hooks := range tests {
		t.Run(name, func(t *testing.T) {
			root, staging := t.TempDir(), t.TempDir()
			victim := filepath.Join(t.TempDir(), "victim")
			writeFixtureFile(t, victim, "victim", 0o600)
			writeFixtureFile(t, filepath.Join(staging, "create"), "created", 0o600)
			before, after := hooks(root, victim)
			setPublishHooks(t, before, after)

			_, err := Root{Path: root}.Apply(t.Context(), Root{Path: staging}, acfstypes.ApplyManifest{
				Version: acfstypes.ProtocolVersion,
				Changes: []acfstypes.ApplyChange{{Operation: acfstypes.ApplyCreateFile, Path: "/file", StagedName: "create", Size: 7}},
			})
			if !errors.Is(err, ErrReplaced) {
				t.Fatalf("Apply error = %v, want ErrReplaced", err)
			}
			if content, readErr := os.ReadFile(victim); readErr != nil || string(content) != "victim" {
				t.Fatalf("victim = %q, %v", content, readErr)
			}
		})
	}
}

// replaceWithLink swaps name for a symbolic link to target, as a concurrent
// writer of the directory could.
func replaceWithLink(t *testing.T, name, target string) {
	t.Helper()
	if err := os.Remove(name); err != nil {
		t.Error(err)
		return
	}
	if err := os.Symlink(target, name); err != nil {
		t.Error(err)
	}
}

func setPublishHooks(t *testing.T, before func(string), after func()) {
	t.Helper()
	beforePublish, afterPublish = before, after
	t.Cleanup(func() {
		beforePublish, afterPublish = nil, nil
	})
}
