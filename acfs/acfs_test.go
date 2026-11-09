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
	"testing"

	acfstypes "kit.getarcane.app/acfs/types"
)

func writeFixtureFile(t testing.TB, filename, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(contents), mode); err != nil {
		t.Fatalf("write fixture %q: %v", filename, err)
	}
}

func TestListEmptyDirectory(t *testing.T) {
	t.Parallel()
	entries, err := Root{Path: t.TempDir()}.List(t.Context(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if entries == nil || len(entries) != 0 {
		t.Fatalf("entries = %#v, want non-nil empty slice", entries)
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
	_, err := Root{Path: root}.Walk(t.Context(), "/", acfstypes.WalkOptions{}, func(entry acfstypes.Entry) error {
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
	_, err = Root{Path: root}.Walk(ctx, "/", acfstypes.WalkOptions{}, func(acfstypes.Entry) error {
		visits++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visits != 1 {
		t.Fatalf("canceled Walk = (visits %d, error %v), want (1, context.Canceled)", visits, err)
	}
}

func TestWalkReportsExactTruncation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "a", "nested", "file"), "x", 0o600)
	writeFixtureFile(t, filepath.Join(root, "root-file"), "x", 0o600)

	var limited []string
	result, err := Root{Path: root}.Walk(t.Context(), "/", acfstypes.WalkOptions{MaxEntries: 2}, func(entry acfstypes.Entry) error {
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
	result, err = Root{Path: root}.Walk(t.Context(), "/", acfstypes.WalkOptions{MaxDepth: 1}, func(entry acfstypes.Entry) error {
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
	result, err = Root{Path: emptyRoot}.Walk(t.Context(), "/", acfstypes.WalkOptions{MaxDepth: 1}, func(acfstypes.Entry) error {
		return nil
	})
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

	written, err := Root{Path: root}.WriteFrom(t.Context(), "/destination", strings.NewReader("replacement"), WriteFromOptions{Size: 11, Mode: 0o640})
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
	err = Root{Path: root}.Write(t.Context(), "/destination", []byte("replacement"), WriteOptions{Mode: 0o640})
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
			_, writeErr := Root{Path: root}.WriteFrom(t.Context(), "/destination", reader, WriteFromOptions{Size: 5, Mode: 0o600})
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

	if err := (Root{Path: root}).WriteAt(ctx, "/chunks", 8, []byte("tail")); err != nil {
		t.Fatalf("WriteAt beyond end: %v", err)
	}
	if err := (Root{Path: root}).WriteAt(ctx, "/chunks", 0, []byte("headpart")); err != nil {
		t.Fatalf("WriteAt at start: %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "headparttail" {
		t.Fatalf("contents = %q, want %q", contents, "headparttail")
	}

	err = Root{Path: root}.WriteAt(ctx, "/missing", 0, []byte("x"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WriteAt on missing file error = %v, want fs.ErrNotExist", err)
	}
	err = Root{Path: root}.WriteAt(ctx, "/", 0, []byte("x"))
	if !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("WriteAt on root error = %v, want ErrIsDirectory", err)
	}
	err = Root{Path: root}.WriteAt(ctx, "/chunks", -1, nil)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("WriteAt with negative offset error = %v, want ErrInvalidPath", err)
	}
	before, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	err = Root{Path: root}.Write(ctx, "/chunks", []byte("short"), WriteOptions{Mode: 0o640, InPlace: true})
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
	err = Root{Path: root}.Write(ctx, "/chunks", []byte("short"), WriteOptions{Mode: 0o400, InPlace: true})
	if err != nil {
		t.Fatalf("identical read-only file: %v", err)
	}
	err = Root{Path: root}.Write(ctx, "/created", []byte("new"), WriteOptions{Mode: 0o750, InPlace: true})
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
	err = Root{Path: root}.Write(ctx, "/link", []byte("changed"), WriteOptions{Mode: 0o600, InPlace: true})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("in-place Write symlink = %v, want ErrSymlink", err)
	}
	contents, err = os.ReadFile(destination)
	if err != nil || string(contents) != "short" {
		t.Fatalf("symlink target changed: %q, %v", contents, err)
	}
	err = Root{Path: root}.Write(ctx, "/.acfs-write-reserved", nil, WriteOptions{Mode: 0o600, InPlace: true})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("in-place Write reserved path = %v, want ErrInvalidPath", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = Root{Path: root}.Write(cancelCtx, "/created", []byte("cancelled"), WriteOptions{Mode: 0o600, InPlace: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-place Write cancelled = %v, want context.Canceled", err)
	}
	err = Root{Path: root}.Write(ctx, "/link", []byte("regular replacement"), WriteOptions{Mode: 0o600})
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

	reader, size, err := Root{Path: root}.OpenReadSeek(ctx, "/payload")
	if err != nil {
		t.Fatalf("OpenReadSeek: %v", err)
	}
	defer func() {
		_ = reader.Close()
	}()
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

	_, _, err = Root{Path: root}.OpenReadSeek(ctx, "/")
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
	if err := (Root{Path: root}).MkdirAll(t.Context(), "/alias/one/two", 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "real", "one", "two")); err != nil || !info.IsDir() {
		t.Fatalf("created directory = (%v, %v)", info, err)
	}
	writeFixtureFile(t, filepath.Join(root, "real", "file"), "file", 0o600)
	for _, logicalPath := range []string{"/real/file", "/alias/file"} {
		if err := (Root{Path: root}).MkdirAll(t.Context(), logicalPath, 0o750); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("MkdirAll(%q) over a file = %v, want fs.ErrExist", logicalPath, err)
		}
	}

	writeFixtureFile(t, filepath.Join(root, "real", "keep"), "keep", 0o600)
	if err := os.Symlink("real/keep", filepath.Join(root, "remove-link")); err != nil {
		t.Fatal(err)
	}
	if err := (Root{Path: root}).RemoveAll(t.Context(), "/remove-link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "remove-link")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("final symlink still exists: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "real", "keep")); err != nil || string(contents) != "keep" {
		t.Fatalf("symlink target changed: (%q, %v)", contents, err)
	}

	if err := (Root{Path: root}).RemoveAll(t.Context(), "/"); !errors.Is(err, ErrRootRemoval) {
		t.Fatalf("RemoveAll(root) = %v, want ErrRootRemoval", err)
	}
	if err := (Root{Path: root}).RemoveAll(t.Context(), "/missing/child"); err != nil {
		t.Fatalf("RemoveAll(missing) = %v, want nil", err)
	}
}

func TestRejectsMalformedAndEscapingPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, logicalPath := range []string{"", "relative", "/../outside", "/a/../b", "/a//b", "/a/./b", "/trailing/", "/nul\x00byte"} {
		_, err := Root{Path: root}.Stat(t.Context(), logicalPath, acfstypes.StatOptions{})
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Stat(%q) error = %v, want ErrInvalidPath", logicalPath, err)
		}
	}

	if err := os.Symlink("../../outside", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	if _, err := (Root{Path: root}).ReadTo(t.Context(), "/escape", &destination, 0); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("ReadTo(escape) = %v, want ErrOutsideRoot", err)
	}
	if err := os.Symlink("/tmp", filepath.Join(root, "absolute-external")); err != nil {
		t.Fatal(err)
	}
	if err := (Root{Path: root}).MkdirAll(t.Context(), "/absolute-external/child", 0o755); !errors.Is(err, ErrOutsideRoot) {
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
	if _, err := (Root{Path: root}).List(t.Context(), "/"); err != nil {
		t.Fatalf("List failed while entries were removed: %v", err)
	}
	<-removeDone
}

func TestListEachSeesChangesMadeByVisit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "parent", "child")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		writeFixtureFile(t, filepath.Join(directory, name), "x", 0o600)
	}
	if err := os.Symlink("../../parent/child/a", filepath.Join(directory, "d")); err != nil {
		t.Fatal(err)
	}

	var visited []acfstypes.Entry
	err := Root{Path: root}.ListEach(t.Context(), "/parent/child", func(entry acfstypes.Entry) error {
		if entry.Name == "a" {
			if err := os.Remove(filepath.Join(directory, "b")); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(directory, "c"), []byte("longer"), 0o644); err != nil {
				return err
			}
		}
		visited = append(visited, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(visited))
	for _, entry := range visited {
		got = append(got, fmt.Sprintf("%s %s %s %d %s", entry.Name, entry.Path, entry.Mode, entry.Size, entry.LinkTarget))
	}
	want := []string{
		"a /parent/child/a -rw------- 1 ",
		"c /parent/child/c -rw------- 6 ",
		"d /parent/child/d lrwxrwxrwx 20 ../../parent/child/a",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ListEach entries = %q, want %q", got, want)
	}
}

func TestReservedWriteNamesAreHiddenAndRejected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	reserved := temporaryWritePrefix + "fixture"
	writeFixtureFile(t, filepath.Join(root, reserved), "internal", 0o600)
	entries, err := Root{Path: root}.List(t.Context(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("reserved entry was listed: %#v", entries)
	}
	_, err = Root{Path: root}.WriteFrom(t.Context(), "/"+reserved, strings.NewReader("x"), WriteFromOptions{Size: 1, Mode: 0o600})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("reserved write error = %v, want ErrInvalidPath", err)
	}
}

func writeBenchmarkEntries(b *testing.B, directory string, count int) {
	b.Helper()
	for index := range count {
		writeFixtureFile(b, filepath.Join(directory, fmt.Sprintf("entry-%06d", index)), "x", 0o600)
	}
}

func writeBenchmarkPayload(b *testing.B, filename string, size int) {
	b.Helper()
	writeFixtureFile(b, filename, strings.Repeat("x", size), 0o600)
}

func BenchmarkList(b *testing.B) {
	for _, size := range []int{10, 100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("entries-%d", size), func(b *testing.B) {
			root := b.TempDir()
			writeBenchmarkEntries(b, root, size)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := (Root{Path: root}).List(b.Context(), "/"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkListEach(b *testing.B) {
	root := b.TempDir()
	writeBenchmarkEntries(b, root, 1_000)
	b.ReportAllocs()
	for b.Loop() {
		err := Root{Path: root}.ListEach(b.Context(), "/", func(acfstypes.Entry) error {
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWalk(b *testing.B) {
	// 6 + 36 + 216 directories and 1728 files: 1986 entries, depth 4.
	root := b.TempDir()
	for first := range 6 {
		for second := range 6 {
			for third := range 6 {
				directory := filepath.Join(root, fmt.Sprintf("a%d", first), fmt.Sprintf("b%d", second), fmt.Sprintf("c%d", third))
				if err := os.MkdirAll(directory, 0o755); err != nil {
					b.Fatal(err)
				}
				writeBenchmarkEntries(b, directory, 8)
			}
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		result, err := Root{Path: root}.Walk(b.Context(), "/", acfstypes.WalkOptions{}, func(acfstypes.Entry) error {
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
		if result.Count != 1_986 {
			b.Fatalf("Walk count = %d, want 1986", result.Count)
		}
	}
}

func newStatBenchmarkRoot(b *testing.B) Root {
	b.Helper()
	root := b.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "project", "config"), 0o755); err != nil {
		b.Fatal(err)
	}
	writeBenchmarkPayload(b, filepath.Join(root, "project", "config", "compose.yaml"), 4<<10)
	if err := os.Symlink("project/config/compose.yaml", filepath.Join(root, "compose.yaml")); err != nil {
		b.Fatal(err)
	}
	return Root{Path: root}
}

func BenchmarkStat(b *testing.B) {
	root := newStatBenchmarkRoot(b)
	b.Run("no-follow", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := root.Stat(b.Context(), "/project/config/compose.yaml", acfstypes.StatOptions{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("follow-symlink", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := root.Stat(b.Context(), "/compose.yaml", acfstypes.StatOptions{FollowFinal: true}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := root.Stat(b.Context(), "/project/config/compose.yaml", acfstypes.StatOptions{}); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})
}

func BenchmarkExists(b *testing.B) {
	root := newStatBenchmarkRoot(b)
	b.ReportAllocs()
	for b.Loop() {
		exists, err := root.Exists(b.Context(), "/project/config/compose.yaml")
		if err != nil || !exists {
			b.Fatalf("Exists = %v, %v", exists, err)
		}
	}
}

func BenchmarkReadFile(b *testing.B) {
	for _, size := range []int{4 << 10, 10 << 20} {
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			root := b.TempDir()
			writeBenchmarkPayload(b, filepath.Join(root, "payload"), size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				if _, err := (Root{Path: root}).ReadFile(b.Context(), "/payload"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("parallel-bytes-4096", func(b *testing.B) {
		root := b.TempDir()
		writeBenchmarkPayload(b, filepath.Join(root, "payload"), 4<<10)
		b.ReportAllocs()
		b.SetBytes(4 << 10)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := (Root{Path: root}).ReadFile(b.Context(), "/payload"); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})
}

func BenchmarkOpenRead(b *testing.B) {
	const size = 10 << 20
	root := b.TempDir()
	writeBenchmarkPayload(b, filepath.Join(root, "payload"), size)
	b.ReportAllocs()
	b.SetBytes(size)
	for b.Loop() {
		reader, _, err := Root{Path: root}.OpenRead(b.Context(), "/payload", 0)
		if err != nil {
			b.Fatal(err)
		}
		_, err = io.Copy(io.Discard, reader)
		if err != nil {
			b.Fatal(err)
		}
		err = reader.Close()
		if err != nil {
			b.Fatal(err)
		}
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
			for b.Loop() {
				_, err = Root{Path: root}.ReadTo(b.Context(), "/payload", io.Discard, 0)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkWrite(b *testing.B) {
	payloads := [2][]byte{bytes.Repeat([]byte("a"), 4<<10), bytes.Repeat([]byte("b"), 4<<10)}
	for _, inPlace := range []bool{false, true} {
		b.Run(fmt.Sprintf("bytes-4096-in-place-%t", inPlace), func(b *testing.B) {
			root := Root{Path: b.TempDir()}
			b.ReportAllocs()
			b.SetBytes(4 << 10)
			iteration := 0
			for b.Loop() {
				// Alternate contents so in-place writes never take the identical-file shortcut.
				err := root.Write(b.Context(), "/payload", payloads[iteration%2], WriteOptions{Mode: 0o600, InPlace: inPlace})
				if err != nil {
					b.Fatal(err)
				}
				iteration++
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
			for b.Loop() {
				source := io.LimitReader(zeroReader{}, size)
				if _, err := (Root{Path: root}).WriteFrom(b.Context(), "/payload", source, WriteFromOptions{Size: size, Mode: 0o600}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMkdirAll(b *testing.B) {
	b.Run("existing", func(b *testing.B) {
		root := Root{Path: b.TempDir()}
		if err := root.MkdirAll(b.Context(), "/a/b/c/d", 0o755); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if err := root.MkdirAll(b.Context(), "/a/b/c/d", 0o755); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("new-depth-4", func(b *testing.B) {
		root := Root{Path: b.TempDir()}
		b.ReportAllocs()
		iteration := 0
		for b.Loop() {
			if err := root.MkdirAll(b.Context(), fmt.Sprintf("/n%d/b/c/d", iteration), 0o755); err != nil {
				b.Fatal(err)
			}
			iteration++
		}
	})
}

func BenchmarkRemoveAll(b *testing.B) {
	root := Root{Path: b.TempDir()}
	tree := filepath.Join(root.Path, "tree")
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		for directory := range 3 {
			nested := filepath.Join(tree, fmt.Sprintf("dir-%d", directory))
			if err := os.MkdirAll(nested, 0o755); err != nil {
				b.Fatal(err)
			}
			writeBenchmarkEntries(b, nested, 3)
		}
		b.StartTimer()
		if err := root.RemoveAll(b.Context(), "/tree"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRemoveSurfacesMissingTargetAndNonEmptyDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()
	if err := os.MkdirAll(filepath.Join(root, "parent", "child"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := (Root{Path: root}).Remove(ctx, "/parent"); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Remove of non-empty directory error = %v, want ErrNotEmpty", err)
	}
	if err := (Root{Path: root}).Remove(ctx, "/parent/child"); err != nil {
		t.Fatalf("Remove of empty directory: %v", err)
	}

	// Unlike RemoveAll, Remove reports a missing target so cleanup loops stop.
	if err := (Root{Path: root}).Remove(ctx, "/parent/child"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Remove of missing target error = %v, want fs.ErrNotExist", err)
	}
	if err := (Root{Path: root}).RemoveAll(ctx, "/parent/child"); err != nil {
		t.Fatalf("RemoveAll of missing target = %v, want nil", err)
	}
}

func TestMkdirRejectsExistingTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()
	if err := (Root{Path: root}).Mkdir(ctx, "/created", 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	err := Root{Path: root}.Mkdir(ctx, "/created", 0o750)
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

	linked, err := Root{Path: root}.Stat(ctx, "/link.yaml", acfstypes.StatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !linked.IsSymlink {
		t.Fatalf("Stat reported IsSymlink = false for a symlink")
	}

	followed, err := Root{Path: root}.Stat(ctx, "/link.yaml", acfstypes.StatOptions{FollowFinal: true})
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

	present, err := Root{Path: root}.Exists(ctx, "/present.txt")
	if err != nil || !present {
		t.Fatalf("Exists(present) = %v, %v; want true, nil", present, err)
	}
	missing, err := Root{Path: root}.Exists(ctx, "/missing/deep.txt")
	if err != nil || missing {
		t.Fatalf("Exists(missing) = %v, %v; want false, nil", missing, err)
	}

	logical, err := Root{Path: root}.LogicalPath(filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if logical != "/nested/file.txt" {
		t.Fatalf("LogicalPath = %q, want %q", logical, "/nested/file.txt")
	}
	_, err = Root{Path: root}.LogicalPath(filepath.Dir(root))
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("LogicalPath escape error = %v, want ErrOutsideRoot", err)
	}

	// A directory whose name merely starts with ".." still lives inside the root.
	dotted, err := Root{Path: root}.LogicalPath(filepath.Join(root, "..hidden", "file"))
	if err != nil {
		t.Fatalf("LogicalPath(..hidden) error = %v, want nil", err)
	}
	if dotted != "/..hidden/file" {
		t.Fatalf("LogicalPath(..hidden) = %q, want %q", dotted, "/..hidden/file")
	}
}

func TestReadFileReturnsEmptyNonNilContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "empty"), "", 0o644)
	content, err := Root{Path: root}.ReadFile(t.Context(), "/empty")
	if err != nil || content == nil || len(content) != 0 {
		t.Fatalf("ReadFile(empty) = %#v, %v; want empty non-nil content", content, err)
	}
}
