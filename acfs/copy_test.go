package acfs

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	acfstypes "go.getarcane.app/acfs/types"
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

	result, err := CopyDir(ctx, source, destination, acfstypes.CopyOptions{TolerateUnreadable: true})
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
	if _, err := os.Lstat(filepath.Join(destination, "link.yaml")); err == nil {
		t.Fatal("CopyDir recreated a symbolic link in the destination")
	}
}

func TestCopyDirRecordsUnreadableFileOnlyWhenTolerant(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced when running as root")
	}
	t.Parallel()

	source := t.TempDir()
	ctx := t.Context()
	writeFixtureFile(t, filepath.Join(source, "compose.yaml"), "services: {}", 0o640)
	if err := os.Mkdir(filepath.Join(source, "secrets"), 0o750); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(source, "secrets", "token.txt")
	writeFixtureFile(t, locked, "supersecret", 0o640)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o640) })

	tolerant, err := CopyDir(ctx, source, t.TempDir(), acfstypes.CopyOptions{TolerateUnreadable: true})
	if err != nil {
		t.Fatalf("tolerant CopyDir: %v", err)
	}
	if !slices.Contains(tolerant.Skipped, filepath.Join("secrets", "token.txt")) {
		t.Fatalf("tolerant CopyDir skipped = %v", tolerant.Skipped)
	}

	if _, err := CopyDir(ctx, source, t.TempDir(), acfstypes.CopyOptions{}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("strict CopyDir error = %v, want a permission error", err)
	}
}

func TestMirrorDirLeavesUnreadableDestinationSubtreeIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced when running as root")
	}
	t.Parallel()

	destination := t.TempDir()
	writeFixtureFile(t, filepath.Join(destination, "compose.yaml"), "old", 0o640)
	locked := filepath.Join(destination, "volumes")
	if err := os.MkdirAll(filepath.Join(locked, "config"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(locked, "config", "app.conf"), "keep", 0o640)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })

	source := t.TempDir()
	writeFixtureFile(t, filepath.Join(source, "compose.yaml"), "new", 0o640)

	// The unreadable live-project subtree must be left intact rather than
	// failing the whole mirror (#3509, #3085).
	if err := MirrorDir(t.Context(), source, destination, acfstypes.MirrorOptions{Preserve: []string{"volumes"}}); err != nil {
		t.Fatalf("MirrorDir: %v", err)
	}
	if err := os.Chmod(locked, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(locked, "config", "app.conf")); err != nil {
		t.Fatalf("unreadable subtree was pruned: %v", err)
	}
}

func TestCopyDirRejectsNestedRoots(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	nested := filepath.Join(source, "inner")
	if err := os.Mkdir(nested, 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := CopyDir(t.Context(), source, nested, acfstypes.CopyOptions{}); err == nil {
		t.Fatal("CopyDir accepted a destination nested inside the source")
	}
}

func TestMirrorDirPreservesListedEntriesAndDestinationInodes(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	destination := t.TempDir()
	ctx := t.Context()

	writeFixtureFile(t, filepath.Join(source, "compose.yaml"), "new", 0o640)

	writeFixtureFile(t, filepath.Join(destination, "compose.yaml"), "old", 0o640)
	writeFixtureFile(t, filepath.Join(destination, "stale.txt"), "stale", 0o640)
	writeFixtureFile(t, filepath.Join(destination, "unreadable.txt"), "kept", 0o640)
	if err := os.Mkdir(filepath.Join(destination, "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(destination, "data", "secret.db"), "kept", 0o600)

	before, err := os.Stat(filepath.Join(destination, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// Files the backup could not read (#3509) must survive the restore, along
	// with the directory that still holds one.
	options := acfstypes.MirrorOptions{Preserve: []string{"unreadable.txt", filepath.Join("data", "secret.db")}}
	if err := MirrorDir(ctx, source, destination, options); err != nil {
		t.Fatalf("MirrorDir: %v", err)
	}

	for _, kept := range []string{"unreadable.txt", filepath.Join("data", "secret.db")} {
		if _, err := os.Stat(filepath.Join(destination, kept)); err != nil {
			t.Fatalf("preserved entry %q was pruned: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "stale.txt")); err == nil {
		t.Fatal("MirrorDir kept an entry absent from the source")
	}

	contents, err := os.ReadFile(filepath.Join(destination, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "new" {
		t.Fatalf("mirrored contents = %q, want %q", contents, "new")
	}

	// Updating in place keeps the inode alive so container bind mounts into
	// the destination stay valid.
	after, err := os.Stat(filepath.Join(destination, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("MirrorDir replaced the destination inode instead of updating in place")
	}
}

func TestMirrorDirSkipsRewritingIdenticalDestinationFiles(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	destination := t.TempDir()
	ctx := t.Context()

	writeFixtureFile(t, filepath.Join(source, "app.conf"), "same", 0o640)
	writeFixtureFile(t, filepath.Join(destination, "app.conf"), "same", 0o640)

	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(destination, "app.conf"), past, past); err != nil {
		t.Fatal(err)
	}

	if err := MirrorDir(ctx, source, destination, acfstypes.MirrorOptions{}); err != nil {
		t.Fatalf("MirrorDir: %v", err)
	}

	info, err := os.Stat(filepath.Join(destination, "app.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatal("MirrorDir rewrote a destination file whose content already matched the source")
	}
}

func TestMirrorDirToleratesUnwritableDestinationFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; file permissions are not enforced")
	}
	t.Parallel()

	source := t.TempDir()
	destination := t.TempDir()
	ctx := t.Context()

	writeFixtureFile(t, filepath.Join(source, "compose.yaml"), "new", 0o640)
	if err := os.Mkdir(filepath.Join(source, "secrets"), 0o750); err != nil {
		t.Fatal(err)
	}
	// The #3625 shape: readable-but-unwritable live files that staging copied
	// verbatim, so their mirrored content is identical.
	writeFixtureFile(t, filepath.Join(source, "secrets", "app.env"), "TOKEN=1", 0o640)
	// And the write-bit-less file whose content genuinely differs: the mirror
	// must carry on and leave the stale content in place.
	writeFixtureFile(t, filepath.Join(source, "secrets", "stale.conf"), "fresh", 0o640)

	writeFixtureFile(t, filepath.Join(destination, "compose.yaml"), "old", 0o640)
	if err := os.Mkdir(filepath.Join(destination, "secrets"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(destination, "secrets", "app.env"), "TOKEN=1", 0o440)
	writeFixtureFile(t, filepath.Join(destination, "secrets", "stale.conf"), "stale", 0o440)

	if err := MirrorDir(ctx, source, destination, acfstypes.MirrorOptions{}); err != nil {
		t.Fatalf("MirrorDir: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(destination, "compose.yaml"):          "new",
		filepath.Join(destination, "secrets", "app.env"):    "TOKEN=1",
		filepath.Join(destination, "secrets", "stale.conf"): "stale",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != want {
			t.Fatalf("%s = %q, want %q", path, contents, want)
		}
	}
}
