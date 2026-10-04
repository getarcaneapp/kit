package acfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	acfstypes "go.getarcane.app/acfs/types"
)

func writeFixtureFile(t *testing.T, filename, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(contents), mode); err != nil {
		t.Fatalf("write fixture %q: %v", filename, err)
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

	entries, err := List(t.Context(), root, "/")
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

	stat, err := Stat(t.Context(), root, "/relative-link", false)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !stat.IsSymlink || stat.LinkTarget != "beta file.txt" {
		t.Fatalf("Stat followed final symlink: %#v", stat)
	}
}

func TestListEmptyDirectory(t *testing.T) {
	t.Parallel()
	entries, err := List(t.Context(), t.TempDir(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if entries == nil || len(entries) != 0 {
		t.Fatalf("entries = %#v, want non-nil empty slice", entries)
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
		written, err := ReadTo(t.Context(), root, logicalPath, &destination, 4)
		if err != nil {
			t.Fatalf("ReadTo(%q): %v", logicalPath, err)
		}
		if written != 4 || destination.String() != "0123" {
			t.Errorf("ReadTo(%q) = (%d, %q), want (4, 0123)", logicalPath, written, destination.String())
		}
	}

	for _, logicalPath := range []string{"/external", "/broken"} {
		var destination bytes.Buffer
		if _, err := ReadTo(t.Context(), root, logicalPath, &destination, 0); err == nil {
			t.Errorf("ReadTo(%q) unexpectedly succeeded", logicalPath)
		}
	}
	var directoryDestination bytes.Buffer
	if _, err := ReadTo(t.Context(), root, "/data", &directoryDestination, 0); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("ReadTo(directory) = %v, want ErrIsDirectory", err)
	}

	if err := os.Symlink("loop-b", filepath.Join(root, "loop-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-a", filepath.Join(root, "loop-b")); err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	_, err := ReadTo(t.Context(), root, "/loop-a", &destination, 0)
	if !errors.Is(err, ErrSymlinkLoop) {
		t.Fatalf("ReadTo(loop) error = %v, want ErrSymlinkLoop", err)
	}
	err = syscall.Mkfifo(filepath.Join(root, "fifo"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = OpenRead(t.Context(), root, "/fifo", 0)
	if !errors.Is(err, ErrNotFile) {
		t.Fatalf("OpenRead(fifo) error = %v, want ErrNotFile", err)
	}
	externalTarget := filepath.Join(t.TempDir(), "external.env")
	writeFixtureFile(t, externalTarget, "ENV=value", 0o600)
	err = os.Symlink(externalTarget, filepath.Join(root, "env"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Stat(t.Context(), root, "/env", true)
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Stat external env = %v, want ErrOutsideRoot", err)
	}
	volumeRoot := filepath.VolumeName(root) + string(filepath.Separator)
	logicalEnv, err := LogicalPath(volumeRoot, filepath.Join(root, "env"))
	if err != nil {
		t.Fatal(err)
	}
	resolvedEnv, err := Stat(t.Context(), volumeRoot, logicalEnv, true)
	if err != nil {
		t.Fatal(err)
	}
	wantEnv, err := filepath.EvalSymlinks(externalTarget)
	if err != nil || filepath.Join(volumeRoot, filepath.FromSlash(resolvedEnv.Path)) != wantEnv {
		t.Fatalf("Stat filesystem root path = %q, want %q (%v)", resolvedEnv.Path, wantEnv, err)
	}
}

func TestWalkIsDeterministicAndDoesNotFollowSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "a", "nested", "file"), "x", 0o600)
	if err := os.Symlink("a", filepath.Join(root, "a-link")); err != nil {
		t.Fatal(err)
	}

	var paths []string
	err := Walk(t.Context(), root, "/", func(entry acfstypes.Entry) error {
		paths = append(paths, entry.Path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/a", "/a-link", "/b", "/a/nested", "/a/nested/file"}
	if !slices.Equal(paths, want) {
		t.Fatalf("walk paths = %q, want %q", paths, want)
	}

	ctx, cancel := context.WithCancel(t.Context())
	visits := 0
	err = Walk(ctx, root, "/", func(acfstypes.Entry) error {
		visits++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visits != 1 {
		t.Fatalf("canceled Walk = (visits %d, error %v), want (1, context.Canceled)", visits, err)
	}
}

func TestWalkBoundedReportsExactTruncation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "a", "nested", "file"), "x", 0o600)
	writeFixtureFile(t, filepath.Join(root, "root-file"), "x", 0o600)

	var limited []string
	result, err := WalkBounded(t.Context(), root, "/", acfstypes.WalkOptions{MaxEntries: 2}, func(entry acfstypes.Entry) error {
		limited = append(limited, entry.Path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 || !result.Truncated || len(limited) != 2 {
		t.Fatalf("entry-bounded walk = (%#v, %q), want two truncated entries", result, limited)
	}

	var depthLimited []string
	result, err = WalkBounded(t.Context(), root, "/", acfstypes.WalkOptions{MaxDepth: 1}, func(entry acfstypes.Entry) error {
		depthLimited = append(depthLimited, entry.Path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || !slices.Equal(depthLimited, []string{"/a", "/root-file"}) {
		t.Fatalf("depth-bounded walk = (%#v, %q)", result, depthLimited)
	}

	emptyRoot := t.TempDir()
	err = os.Mkdir(filepath.Join(emptyRoot, "empty"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	result, err = WalkBounded(t.Context(), emptyRoot, "/", acfstypes.WalkOptions{MaxDepth: 1}, func(acfstypes.Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Truncated {
		t.Fatal("empty directory at the depth boundary was incorrectly reported as truncated")
	}
}

type failingReader struct {
	remaining int
}

func (r *failingReader) Read(buffer []byte) (int, error) {
	if r.remaining == 0 {
		return 0, errors.New("fixture interrupted")
	}
	if len(buffer) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	for index := range buffer {
		buffer[index] = 'x'
	}
	r.remaining -= len(buffer)
	return len(buffer), nil
}

func assertNoTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), temporaryWritePrefix) {
			t.Errorf("temporary file was not removed: %s", entry.Name())
		}
	}
}

func TestWriteFromIsExactAndAtomic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	writeFixtureFile(t, destination, "original", 0o600)

	written, err := WriteFrom(t.Context(), root, "/destination", strings.NewReader("replacement"), 11, 0o640)
	if err != nil {
		t.Fatalf("WriteFrom: %v", err)
	}
	if written != 11 {
		t.Errorf("written = %d, want 11", written)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "replacement" {
		t.Errorf("contents = %q, want replacement", contents)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %o, want 640", info.Mode().Perm())
	}
	err = Write(t.Context(), root, "/destination", []byte("replacement"), WriteOptions{Mode: 0o640})
	if err != nil {
		t.Fatalf("atomic Write: %v", err)
	}
	replaced, err := os.Stat(destination)
	if err != nil || os.SameFile(info, replaced) {
		t.Fatalf("atomic Write did not replace inode: %v", err)
	}

	for name, reader := range map[string]io.Reader{
		"short":       strings.NewReader("tiny"),
		"excess":      strings.NewReader("too-long"),
		"interrupted": &failingReader{remaining: 2},
	} {
		t.Run(name, func(t *testing.T) {
			before, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			_, writeErr := WriteFrom(t.Context(), root, "/destination", reader, 5, 0o600)
			if writeErr == nil {
				t.Fatal("WriteFrom unexpectedly succeeded")
			}
			after, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("destination changed after failed write: %q -> %q", before, after)
			}
			assertNoTemporaryFiles(t, root)
		})
	}
}

func TestWriteAtWritesInPlaceAndGrowsSparsely(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	destination := filepath.Join(root, "chunks")
	writeFixtureFile(t, destination, "", 0o600)
	ctx := t.Context()

	if err := WriteAt(ctx, root, "/chunks", 8, []byte("tail")); err != nil {
		t.Fatalf("WriteAt beyond end: %v", err)
	}
	if err := WriteAt(ctx, root, "/chunks", 0, []byte("headpart")); err != nil {
		t.Fatalf("WriteAt at start: %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "headparttail" {
		t.Fatalf("contents = %q, want %q", contents, "headparttail")
	}

	err = WriteAt(ctx, root, "/missing", 0, []byte("x"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WriteAt on missing file error = %v, want fs.ErrNotExist", err)
	}
	err = WriteAt(ctx, root, "/", 0, []byte("x"))
	if !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("WriteAt on root error = %v, want ErrIsDirectory", err)
	}
	err = WriteAt(ctx, root, "/chunks", -1, nil)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("WriteAt with negative offset error = %v, want ErrInvalidPath", err)
	}
	before, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	err = Write(ctx, root, "/chunks", []byte("short"), WriteOptions{Mode: 0o640, InPlace: true})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(destination)
	if err != nil || string(contents) != "short" || !os.SameFile(before, after) || after.Mode().Perm() != 0o640 {
		t.Fatalf("in-place Write failed inode, contents, or mode preservation: contents=%q, err=%v", contents, err)
	}
	err = os.Chmod(destination, 0o400)
	if err != nil {
		t.Fatal(err)
	}
	err = Write(ctx, root, "/chunks", []byte("short"), WriteOptions{Mode: 0o400, InPlace: true})
	if err != nil {
		t.Fatalf("identical read-only file: %v", err)
	}
	err = Write(ctx, root, "/created", []byte("new"), WriteOptions{Mode: 0o750, InPlace: true})
	if err != nil {
		t.Fatal(err)
	}
	created, err := os.Stat(filepath.Join(root, "created"))
	if err != nil || created.Mode().Perm() != 0o750 {
		t.Fatalf("created file mode = %v, err=%v", created, err)
	}
	err = os.Symlink("chunks", filepath.Join(root, "link"))
	if err != nil {
		t.Fatal(err)
	}
	err = Write(ctx, root, "/link", []byte("changed"), WriteOptions{Mode: 0o600, InPlace: true})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("in-place Write symlink = %v, want ErrSymlink", err)
	}
	contents, err = os.ReadFile(destination)
	if err != nil || string(contents) != "short" {
		t.Fatalf("symlink target changed: %q, %v", contents, err)
	}
	err = Write(ctx, root, "/.acfs-write-reserved", nil, WriteOptions{Mode: 0o600, InPlace: true})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("in-place Write reserved path = %v, want ErrInvalidPath", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = Write(cancelCtx, root, "/created", []byte("cancelled"), WriteOptions{Mode: 0o600, InPlace: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-place Write cancelled = %v, want context.Canceled", err)
	}
	err = Write(ctx, root, "/link", []byte("regular replacement"), WriteOptions{Mode: 0o600})
	if err != nil {
		t.Fatalf("atomic Write should replace final symlink: %v", err)
	}
	linkInfo, err := os.Lstat(filepath.Join(root, "link"))
	if err != nil || !linkInfo.Mode().IsRegular() {
		t.Fatalf("atomic Write destination is not regular: %v", err)
	}
	contents, err = os.ReadFile(destination)
	if err != nil || string(contents) != "short" {
		t.Fatalf("atomic Write changed symlink target: %q, %v", contents, err)
	}
}

func TestOpenReadSeekReadsAndRewinds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "payload"), "seekable-contents", 0o600)
	ctx := t.Context()

	reader, size, err := OpenReadSeek(ctx, root, "/payload")
	if err != nil {
		t.Fatalf("OpenReadSeek: %v", err)
	}
	defer func() { _ = reader.Close() }()
	if size != int64(len("seekable-contents")) {
		t.Fatalf("size = %d, want %d", size, len("seekable-contents"))
	}

	first, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := reader.Seek(0, io.SeekStart)
	if err != nil || offset != 0 {
		t.Fatalf("Seek = %d, %v", offset, err)
	}
	second, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "seekable-contents" || !bytes.Equal(first, second) {
		t.Fatalf("reads differ: %q vs %q", first, second)
	}

	_, _, err = OpenReadSeek(ctx, root, "/")
	if !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("OpenReadSeek on directory error = %v, want ErrIsDirectory", err)
	}
}

func TestMkdirAndRemoveStayInsideRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(t.Context(), root, "/alias/one/two", 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "real", "one", "two")); err != nil || !info.IsDir() {
		t.Fatalf("created directory = (%v, %v)", info, err)
	}

	writeFixtureFile(t, filepath.Join(root, "real", "keep"), "keep", 0o600)
	if err := os.Symlink("real/keep", filepath.Join(root, "remove-link")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(t.Context(), root, "/remove-link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "remove-link")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("final symlink still exists: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "real", "keep")); err != nil || string(contents) != "keep" {
		t.Fatalf("symlink target changed: (%q, %v)", contents, err)
	}

	if err := RemoveAll(t.Context(), root, "/"); !errors.Is(err, ErrRootRemoval) {
		t.Fatalf("RemoveAll(root) = %v, want ErrRootRemoval", err)
	}
	if err := RemoveAll(t.Context(), root, "/missing/child"); err != nil {
		t.Fatalf("RemoveAll(missing) = %v, want nil", err)
	}
}

func TestRejectsMalformedAndEscapingPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, logicalPath := range []string{"", "relative", "/../outside", "/a/../b", "/a//b", "/a/./b", "/trailing/", "/nul\x00byte"} {
		_, err := Stat(t.Context(), root, logicalPath, false)
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Stat(%q) error = %v, want ErrInvalidPath", logicalPath, err)
		}
	}

	if err := os.Symlink("../../outside", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	if _, err := ReadTo(t.Context(), root, "/escape", &destination, 0); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("ReadTo(escape) = %v, want ErrOutsideRoot", err)
	}
	if err := os.Symlink("/tmp", filepath.Join(root, "absolute-external")); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(t.Context(), root, "/absolute-external/child", 0o755); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("MkdirAll(external) = %v, want ErrOutsideRoot", err)
	}
}

func TestListToleratesConcurrentRemoval(t *testing.T) {
	root := t.TempDir()
	for index := range 2_000 {
		writeFixtureFile(t, filepath.Join(root, fmt.Sprintf("entry-%04d", index)), "x", 0o600)
	}

	removeDone := make(chan struct{})
	go func() {
		defer close(removeDone)
		for index := range 2_000 {
			_ = os.Remove(filepath.Join(root, fmt.Sprintf("entry-%04d", index)))
		}
	}()
	if _, err := List(t.Context(), root, "/"); err != nil {
		t.Fatalf("List failed while entries were removed: %v", err)
	}
	<-removeDone
}

func TestReservedWriteNamesAreHiddenAndRejected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	reserved := temporaryWritePrefix + "fixture"
	writeFixtureFile(t, filepath.Join(root, reserved), "internal", 0o600)
	entries, err := List(t.Context(), root, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("reserved entry was listed: %#v", entries)
	}
	_, err = WriteFrom(t.Context(), root, "/"+reserved, strings.NewReader("x"), 1, 0o600)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("reserved write error = %v, want ErrInvalidPath", err)
	}
}

func BenchmarkList(b *testing.B) {
	for _, size := range []int{10, 100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("entries-%d", size), func(b *testing.B) {
			root := b.TempDir()
			for index := range size {
				filename := filepath.Join(root, fmt.Sprintf("entry-%06d", index))
				if err := os.WriteFile(filename, []byte("x"), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for range b.N {
				if _, err := List(b.Context(), root, "/"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func BenchmarkReadTo(b *testing.B) {
	for _, size := range []int64{1 << 20, 100 << 20} {
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			root := b.TempDir()
			file, err := os.Create(filepath.Join(root, "payload"))
			if err != nil {
				b.Fatal(err)
			}
			err = file.Truncate(size)
			if err != nil {
				b.Fatal(err)
			}
			err = file.Close()
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(size)
			b.ResetTimer()
			for range b.N {
				_, err = ReadTo(b.Context(), root, "/payload", io.Discard, 0)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkWriteFrom(b *testing.B) {
	for _, size := range []int64{1 << 20, 100 << 20} {
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			root := b.TempDir()
			b.ReportAllocs()
			b.SetBytes(size)
			b.ResetTimer()
			for range b.N {
				source := io.LimitReader(zeroReader{}, size)
				if _, err := WriteFrom(b.Context(), root, "/payload", source, size, 0o600); err != nil {
					b.Fatal(err)
				}
			}
		})
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
	_, err = LogicalPath(root, filepath.Dir(root))
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("LogicalPath escape error = %v, want ErrOutsideRoot", err)
	}

	// A directory whose name merely starts with ".." still lives inside the root.
	dotted, err := LogicalPath(root, filepath.Join(root, "..hidden", "file"))
	if err != nil {
		t.Fatalf("LogicalPath(..hidden) error = %v, want nil", err)
	}
	if dotted != "/..hidden/file" {
		t.Fatalf("LogicalPath(..hidden) = %q, want %q", dotted, "/..hidden/file")
	}
}
