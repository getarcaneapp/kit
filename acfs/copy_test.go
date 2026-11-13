package acfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	acfstypes "kit.getarcane.app/acfs/types"
)

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
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o640)
	})

	tolerant, err := Root{Path: source}.CopyTo(ctx, Root{Path: t.TempDir()}, acfstypes.CopyOptions{TolerateUnreadable: true})
	if err != nil {
		t.Fatalf("tolerant CopyDir: %v", err)
	}
	if !slices.Contains(tolerant.Skipped, filepath.Join("secrets", "token.txt")) {
		t.Fatalf("tolerant CopyDir skipped = %v", tolerant.Skipped)
	}

	_, err = Root{Path: source}.CopyTo(ctx, Root{Path: t.TempDir()}, acfstypes.CopyOptions{})
	if !errors.Is(err, os.ErrPermission) {
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
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o750)
	})

	source := t.TempDir()
	writeFixtureFile(t, filepath.Join(source, "compose.yaml"), "new", 0o640)

	// The unreadable live-project subtree must be left intact rather than
	// failing the whole mirror (#3509, #3085).
	if err := (Root{Path: source}).MirrorTo(t.Context(), (Root{Path: destination}), acfstypes.MirrorOptions{Preserve: []string{"volumes"}}); err != nil {
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

	if _, err := (Root{Path: source}).CopyTo(t.Context(), (Root{Path: nested}), acfstypes.CopyOptions{}); err == nil {
		t.Fatal("CopyDir accepted a destination nested inside the source")
	}
}

func TestCopyDirAcceptsSiblingWithDotDotPrefix(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "src")
	destination := filepath.Join(root, "..dst")
	for _, dir := range []string{source, destination} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(source, "compose.yaml"), "services: {}", 0o640)

	result, err := Root{Path: source}.CopyTo(t.Context(), Root{Path: destination}, acfstypes.CopyOptions{})
	if err != nil {
		t.Fatalf("CopyDir rejected a sibling destination named ..dst: %v", err)
	}
	if result.Copied != 1 {
		t.Fatalf("CopyDir copied = %d, want 1", result.Copied)
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
	err = Root{Path: source}.MirrorTo(ctx, Root{Path: destination}, options)
	if err != nil {
		t.Fatalf("MirrorDir: %v", err)
	}

	for _, kept := range []string{"unreadable.txt", filepath.Join("data", "secret.db")} {
		_, err = os.Stat(filepath.Join(destination, kept))
		if err != nil {
			t.Fatalf("preserved entry %q was pruned: %v", kept, err)
		}
	}
	_, err = os.Stat(filepath.Join(destination, "stale.txt"))
	if err == nil {
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

	if err := (Root{Path: source}).MirrorTo(ctx, (Root{Path: destination}), acfstypes.MirrorOptions{}); err != nil {
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

func TestMirrorDirRewritesFileDifferingAfterFirstChunk(t *testing.T) {
	t.Parallel()

	source, destination := t.TempDir(), t.TempDir()
	content := strings.Repeat("a", 3*compareChunkSize)
	writeFixtureFile(t, filepath.Join(source, "data"), content+"new", 0o640)
	writeFixtureFile(t, filepath.Join(destination, "data"), content+"old", 0o640)

	if err := (Root{Path: source}).MirrorTo(t.Context(), Root{Path: destination}, acfstypes.MirrorOptions{}); err != nil {
		t.Fatalf("MirrorTo: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content+"new" {
		t.Fatalf("mirrored %d bytes ending %q, want source content", len(got), got[max(len(got)-3, 0):])
	}
}

func TestEqualContent(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 5)
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "empty", want: true},
		{name: "equal across chunks", a: long, b: long, want: true},
		{name: "differs in last chunk", a: long, b: long[:4] + "y"},
		{name: "prefix", a: long, b: long[:4]},
		{name: "chunk aligned prefix", a: long[:4], b: long[:2]},
	}
	for _, test := range tests {
		got, err := equalContent(strings.NewReader(test.a), strings.NewReader(test.b), make([]byte, 4))
		if err != nil || got != test.want {
			t.Errorf("%s: equalContent = %v, %v, want %v", test.name, got, err, test.want)
		}
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

	if err := (Root{Path: source}).MirrorTo(ctx, (Root{Path: destination}), acfstypes.MirrorOptions{}); err != nil {
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

func BenchmarkCopyTo(b *testing.B) {
	// 8 directories of 25 files each: 200 files.
	source := b.TempDir()
	for directory := range 8 {
		nested := filepath.Join(source, fmt.Sprintf("dir-%d", directory))
		if err := os.Mkdir(nested, 0o755); err != nil {
			b.Fatal(err)
		}
		for file := range 25 {
			writeFixtureFile(b, filepath.Join(nested, fmt.Sprintf("file-%02d", file)), strings.Repeat("x", 1<<10), 0o644)
		}
	}
	destination := b.TempDir()
	b.ReportAllocs()
	for b.Loop() {
		result, err := Root{Path: source}.CopyTo(b.Context(), Root{Path: destination}, acfstypes.CopyOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if result.Copied != 200 {
			b.Fatalf("CopyTo copied %d files, want 200", result.Copied)
		}
	}
}

func TestCopyToRefusesDestinationSymlinks(t *testing.T) {
	t.Parallel()

	for _, link := range []string{"file", "nested"} {
		source, destination, outside := t.TempDir(), t.TempDir(), t.TempDir()
		if err := os.Mkdir(filepath.Join(source, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(source, "file"), "source", 0o644)
		writeFixtureFile(t, filepath.Join(source, "nested", "file"), "source", 0o644)
		writeFixtureFile(t, filepath.Join(outside, "file"), "victim", 0o644)
		target := outside
		if link == "file" {
			target = filepath.Join(outside, "file")
		}
		if err := os.Symlink(target, filepath.Join(destination, link)); err != nil {
			t.Fatal(err)
		}

		_, err := Root{Path: source}.CopyTo(t.Context(), Root{Path: destination}, acfstypes.CopyOptions{})
		if !errors.Is(err, ErrSymlink) {
			t.Fatalf("CopyTo through destination link %q = %v, want ErrSymlink", link, err)
		}
		if content, readErr := os.ReadFile(filepath.Join(outside, "file")); readErr != nil || string(content) != "victim" {
			t.Fatalf("victim = %q, %v", content, readErr)
		}
	}
}

func TestMirrorToReplacesDestinationSymlinks(t *testing.T) {
	t.Parallel()

	source, destination, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeFixtureFile(t, filepath.Join(source, "file"), "source", 0o644)
	writeFixtureFile(t, filepath.Join(outside, "file"), "victim", 0o644)
	if err := os.Symlink(filepath.Join(outside, "file"), filepath.Join(destination, "file")); err != nil {
		t.Fatal(err)
	}

	if err := (Root{Path: source}).MirrorTo(t.Context(), Root{Path: destination}, acfstypes.MirrorOptions{}); err != nil {
		t.Fatalf("MirrorTo: %v", err)
	}
	info, err := os.Lstat(filepath.Join(destination, "file"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("destination = %v, %v; want a regular file", info, err)
	}
	if content, readErr := os.ReadFile(filepath.Join(outside, "file")); readErr != nil || string(content) != "victim" {
		t.Fatalf("victim = %q, %v", content, readErr)
	}
}
