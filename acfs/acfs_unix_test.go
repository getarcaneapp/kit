//go:build unix

package acfs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	acfstypes "kit.getarcane.app/acfs/types"
)

func TestListAndStat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "beta file.txt"), "beta", 0o640)
	newlineName := "alpha\nfile"
	writeFixtureFile(t, filepath.Join(root, newlineName), "alpha", 0o600)
	writeFixtureFile(t, filepath.Join(root, "世界.txt"), "unicode", 0o644)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("beta file.txt", filepath.Join(root, "relative-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/volume/nested", filepath.Join(root, "volume-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "external-link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "named-pipe"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := Root{Path: root}.List(t.Context(), "/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
		if entry.ModTime.Nanosecond() != 0 {
			t.Errorf("modification time was not rounded to seconds: %v", entry.ModTime)
		}
	}
	if !slices.IsSorted(names) {
		t.Fatalf("entries are not sorted: %q", names)
	}

	regular := findEntry(t, entries, "beta file.txt")
	if regular.Path != "/beta file.txt" || regular.Size != 4 || regular.Mode != "-rw-r-----" {
		t.Errorf("unexpected regular entry: %#v", regular)
	}
	if regular.ModTimeUnixNano == 0 {
		t.Error("regular entry did not preserve nanosecond revision metadata")
	}
	directory := findEntry(t, entries, "nested")
	if !directory.IsDirectory || directory.IsSymlink || !strings.HasPrefix(directory.Mode, "d") {
		t.Errorf("unexpected directory entry: %#v", directory)
	}
	relativeLink := findEntry(t, entries, "relative-link")
	if !relativeLink.IsSymlink || relativeLink.LinkTarget != "beta file.txt" || relativeLink.Mode[0] != 'l' {
		t.Errorf("unexpected relative link: %#v", relativeLink)
	}
	if target := findEntry(t, entries, "volume-link").LinkTarget; target != "/nested" {
		t.Errorf("volume link target = %q, want /nested", target)
	}
	if target := findEntry(t, entries, "external-link").LinkTarget; target != "(external)" {
		t.Errorf("external link target = %q, want (external)", target)
	}
	if namedPipe := findEntry(t, entries, "named-pipe"); namedPipe.Mode != "prw-------" {
		t.Errorf("unexpected named pipe: %#v", namedPipe)
	}

	stat, err := Root{Path: root}.Stat(t.Context(), "/relative-link", acfstypes.StatOptions{})
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !stat.IsSymlink || stat.LinkTarget != "beta file.txt" {
		t.Fatalf("Stat followed final symlink: %#v", stat)
	}
}

func TestReadToResolvesOnlyInternalSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "data", "payload"), "0123456789", 0o600)
	links := map[string]string{
		"relative": "data/payload",
		"volume":   "/volume/data/payload",
		"chain":    "relative",
		"external": "/etc/passwd",
		"broken":   "missing",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}

	for _, logicalPath := range []string{"/relative", "/volume", "/chain"} {
		var destination bytes.Buffer
		written, err := Root{Path: root}.ReadTo(t.Context(), logicalPath, &destination, 4)
		if err != nil {
			t.Fatalf("ReadTo(%q): %v", logicalPath, err)
		}
		if written != 4 || destination.String() != "0123" {
			t.Errorf("ReadTo(%q) = (%d, %q), want (4, 0123)", logicalPath, written, destination.String())
		}
	}

	for _, logicalPath := range []string{"/external", "/broken"} {
		var destination bytes.Buffer
		if _, err := (Root{Path: root}).ReadTo(t.Context(), logicalPath, &destination, 0); err == nil {
			t.Errorf("ReadTo(%q) unexpectedly succeeded", logicalPath)
		}
	}
	var directoryDestination bytes.Buffer
	if _, err := (Root{Path: root}).ReadTo(t.Context(), "/data", &directoryDestination, 0); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("ReadTo(directory) = %v, want ErrIsDirectory", err)
	}

	if err := os.Symlink("loop-b", filepath.Join(root, "loop-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-a", filepath.Join(root, "loop-b")); err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	_, err := Root{Path: root}.ReadTo(t.Context(), "/loop-a", &destination, 0)
	if !errors.Is(err, ErrSymlinkLoop) {
		t.Fatalf("ReadTo(loop) error = %v, want ErrSymlinkLoop", err)
	}
	err = syscall.Mkfifo(filepath.Join(root, "fifo"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Root{Path: root}.OpenRead(t.Context(), "/fifo", 0)
	if !errors.Is(err, ErrNotFile) {
		t.Fatalf("OpenRead(fifo) error = %v, want ErrNotFile", err)
	}
	externalTarget := filepath.Join(t.TempDir(), "external.env")
	writeFixtureFile(t, externalTarget, "ENV=value", 0o600)
	err = os.Symlink(externalTarget, filepath.Join(root, "env"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Root{Path: root}.Stat(t.Context(), "/env", acfstypes.StatOptions{FollowFinal: true})
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Stat external env = %v, want ErrOutsideRoot", err)
	}
	volumeRoot := filepath.VolumeName(root) + string(filepath.Separator)
	logicalEnv, err := Root{Path: volumeRoot}.LogicalPath(filepath.Join(root, "env"))
	if err != nil {
		t.Fatal(err)
	}
	resolvedEnv, err := Root{Path: volumeRoot}.Stat(t.Context(), logicalEnv, acfstypes.StatOptions{FollowFinal: true})
	if err != nil {
		t.Fatal(err)
	}
	wantEnv, err := filepath.EvalSymlinks(externalTarget)
	if err != nil || filepath.Join(volumeRoot, filepath.FromSlash(resolvedEnv.Path)) != wantEnv {
		t.Fatalf("Stat filesystem root path = %q, want %q (%v)", resolvedEnv.Path, wantEnv, err)
	}
}

func findEntry(t *testing.T, entries []acfstypes.Entry, name string) acfstypes.Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("entry %q not found in %#v", name, entries)
	return acfstypes.Entry{}
}
