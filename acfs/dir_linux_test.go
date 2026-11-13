//go:build linux

package acfs

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	acfstypes "kit.getarcane.app/acfs/types"
)

// TestMain runs the suite with openat2 and again with the openat walk forced,
// so conformance and race tests cover both resolution modes. Benchmarks run
// once.
func TestMain(m *testing.M) {
	code := m.Run()
	if code != 0 || flag.Lookup("test.bench").Value.String() != "" {
		os.Exit(code)
	}
	forceOpenatWalk = true
	os.Exit(m.Run())
}

// raceLayout creates a/b/file and the sibling c/file under a fresh root.
func raceLayout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"a/b", "c"} {
		if err := os.MkdirAll(inRoot(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, inRoot(root, "a/b/file"), "original")
	writeTestFile(t, inRoot(root, "c/file"), "sibling")
	return root
}

// inRoot returns the host path of the slash-separated name under root.
func inRoot(root, name string) string { return filepath.Join(root, filepath.FromSlash(name)) }

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// swapAfterOpen moves root/a/b aside to a/b.orig the first time a mutation
// opens it and, when linkTarget is set, replaces it with a symbolic link to
// linkTarget, racing the mutation between open and use.
func swapAfterOpen(t *testing.T, root, linkTarget string) {
	t.Helper()
	const relativeDir = "a/b"
	swapped := false
	afterOpenDir = func(opened string) {
		if swapped || opened != relativeDir {
			return
		}
		swapped = true
		current := inRoot(root, relativeDir)
		if err := os.Rename(current, current+".orig"); err != nil {
			t.Error(err)
			return
		}
		if linkTarget == "" {
			return
		}
		if err := os.Symlink(linkTarget, current); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() {
		afterOpenDir = nil
		if !swapped {
			t.Error("directory was never swapped")
		}
	})
}

func readTestFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func requireMissing(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists or failed: %v", name, err)
	}
}

func TestRootSymlinkRaceCannotEscape(t *testing.T) {
	tests := []struct {
		name       string
		linkTarget string
		// escaped is where a link following the swap would have landed.
		escaped string
	}{
		{name: "parent swapped for link to root", linkTarget: "..", escaped: "link"},
		{name: "parent swapped for link to sibling", linkTarget: "../c", escaped: "c/link"},
		{name: "parent relocated", escaped: "a/b/link"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := raceLayout(t)
			swapAfterOpen(t, root, test.linkTarget)

			err := Root{Path: root}.Symlink(t.Context(), "/c/file", "/a/b/link")
			if !errors.Is(err, ErrOutsideRoot) {
				t.Fatalf("Symlink error = %v, want ErrOutsideRoot", err)
			}
			requireMissing(t, inRoot(root, test.escaped))
			requireMissing(t, inRoot(root, "a/b.orig/link"))
		})
	}
}

func TestRootSymlinkRaceRelocatedAncestor(t *testing.T) {
	root := raceLayout(t)
	swapAfterOpen(t, root, "")
	afterSwap := afterOpenDir
	afterOpenDir = func(opened string) {
		afterSwap(opened)
		// Relocate the opened directory one level up, where the stored
		// "../../c/file" would resolve outside the root.
		if opened == "a/b" {
			if err := os.Rename(inRoot(root, "a/b.orig"), inRoot(root, "moved")); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Error(err)
			}
		}
	}

	err := Root{Path: root}.Symlink(t.Context(), "/c/file", "/a/b/link")
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Symlink error = %v, want ErrOutsideRoot", err)
	}
	requireMissing(t, inRoot(root, "moved/link"))
}

func TestRootOpenFileRaceLeavesSibling(t *testing.T) {
	root := raceLayout(t)
	swapAfterOpen(t, root, "../c")

	file, err := Root{Path: root}.OpenFile(t.Context(), "/a/b/file", OpenOptions{Write: true, Truncate: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("written"); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, inRoot(root, "c/file")); got != "sibling" {
		t.Fatalf("sibling = %q, want unmodified", got)
	}
	if got := readTestFile(t, inRoot(root, "a/b.orig/file")); got != "written" {
		t.Fatalf("original = %q, want written", got)
	}
}

func TestCopyToRaceDoesNotListOutsideRoot(t *testing.T) {
	source, destination, host := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(host, "host-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(host, "host.sock"), 0o600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	afterOpenDir = func(opened string) {
		if swapped || opened != "data" {
			return
		}
		swapped = true
		current := filepath.Join(source, "data")
		if err := os.Rename(current, current+".orig"); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink(host, current); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() {
		afterOpenDir = nil
	})

	result, _ := Root{Path: source}.CopyTo(t.Context(), Root{Path: destination}, acfstypes.CopyOptions{TolerateUnreadable: true})
	if !swapped {
		t.Fatal("directory was never swapped")
	}
	if _, err := os.Lstat(filepath.Join(destination, "data", "host-dir")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("host directory recreated in destination: %v", err)
	}
	for _, skipped := range result.Skipped {
		if strings.Contains(skipped, "host") {
			t.Fatalf("host entry listed in Skipped = %v", result.Skipped)
		}
	}
}

// requireNotOpened fails t if run opens the file name.
func requireNotOpened(t *testing.T, name string, run func()) {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		t.Skipf("inotify unavailable: %v", err)
	}
	defer func() {
		_ = unix.Close(fd)
	}()
	if _, err = unix.InotifyAddWatch(fd, name, unix.IN_OPEN); err != nil {
		t.Fatal(err)
	}
	run()
	var events [unix.SizeofInotifyEvent + unix.PathMax]byte
	if n, readErr := unix.Read(fd, events[:]); n > 0 {
		t.Errorf("%s was opened", name)
	} else if !errors.Is(readErr, unix.EAGAIN) {
		t.Fatal(readErr)
	}
}

func TestNonRegularFilesAreNeverOpened(t *testing.T) {
	t.Parallel()

	source, destination := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(source, "file"), "source")
	if err := unix.Mkfifo(filepath.Join(source, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(destination, "file"), 0o600); err != nil {
		t.Fatal(err)
	}

	requireNotOpened(t, filepath.Join(source, "fifo"), func() {
		if _, _, err := (Root{Path: source}).OpenReadSeek(t.Context(), "/fifo"); !errors.Is(err, ErrNotFile) {
			t.Errorf("OpenReadSeek fifo = %v, want ErrNotFile", err)
		}
	})
	requireNotOpened(t, filepath.Join(destination, "file"), func() {
		if _, err := (Root{Path: source}).CopyTo(t.Context(), Root{Path: destination}, acfstypes.CopyOptions{}); !errors.Is(err, ErrNotFile) {
			t.Errorf("CopyTo onto fifo = %v, want ErrNotFile", err)
		}
	})
}

func TestRootAttributeRaceLeavesSibling(t *testing.T) {
	stamp := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	tests := []struct {
		change func(Root) error
		name   string
	}{
		{name: "chmod", change: func(r Root) error {
			return r.Chmod(t.Context(), "/a/b/file", 0o600)
		}},
		{name: "chown", change: func(r Root) error {
			return r.Chown(t.Context(), "/a/b/file", os.Getuid(), os.Getgid())
		}},
		{name: "lchown", change: func(r Root) error {
			return r.Lchown(t.Context(), "/a/b/file", os.Getuid(), os.Getgid())
		}},
		{name: "chtimes", change: func(r Root) error {
			return r.Chtimes(t.Context(), "/a/b/file", stamp, stamp)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := raceLayout(t)
			sibling := inRoot(root, "c/file")
			before := statTestFile(t, sibling)
			swapAfterOpen(t, root, "../c")

			if err := test.change(Root{Path: root}); err != nil {
				t.Fatal(err)
			}
			after := statTestFile(t, sibling)
			if after.Mode != before.Mode || after.Mtim != before.Mtim || after.Ctim != before.Ctim {
				t.Fatalf("sibling changed: before %+v after %+v", before, after)
			}
		})
	}
}

func statTestFile(t *testing.T, name string) syscall.Stat_t {
	t.Helper()
	var stat syscall.Stat_t
	if err := syscall.Stat(name, &stat); err != nil {
		t.Fatal(err)
	}
	return stat
}

func TestRootWriteRaceUsesOpenedDirectory(t *testing.T) {
	for _, test := range []struct {
		name    string
		inPlace bool
	}{{name: "atomic"}, {name: "in place", inPlace: true}} {
		t.Run(test.name, func(t *testing.T) {
			root := raceLayout(t)
			swapAfterOpen(t, root, "../c")

			err := Root{Path: root}.Write(t.Context(), "/a/b/file", []byte("written"), WriteOptions{Mode: 0o644, InPlace: test.inPlace})
			if err != nil {
				t.Fatal(err)
			}
			if got := readTestFile(t, inRoot(root, "c/file")); got != "sibling" {
				t.Fatalf("sibling = %q, want unmodified", got)
			}
			if got := readTestFile(t, inRoot(root, "a/b.orig/file")); got != "written" {
				t.Fatalf("original = %q, want written", got)
			}
		})
	}
}

func TestRootMutationRaceUsesOpenedDirectory(t *testing.T) {
	tests := []struct {
		mutate func(t *testing.T, r Root) error
		check  func(t *testing.T, root string)
		name   string
	}{
		{
			name: "rename into swapped parent",
			mutate: func(t *testing.T, r Root) error {
				return r.Rename(t.Context(), "/c/file", "/a/b/renamed")
			},
			check: func(t *testing.T, root string) {
				t.Helper()
				if got := readTestFile(t, inRoot(root, "a/b.orig/renamed")); got != "sibling" {
					t.Fatalf("renamed = %q", got)
				}
			},
		},
		{
			name: "remove",
			mutate: func(t *testing.T, r Root) error {
				return r.Remove(t.Context(), "/a/b/file")
			},
			check: func(t *testing.T, root string) {
				t.Helper()
				requireMissing(t, inRoot(root, "a/b.orig/file"))
				readTestFile(t, inRoot(root, "c/file"))
			},
		},
		{
			name: "remove all",
			mutate: func(t *testing.T, r Root) error {
				return r.RemoveAll(t.Context(), "/a/b/file")
			},
			check: func(t *testing.T, root string) {
				t.Helper()
				requireMissing(t, inRoot(root, "a/b.orig/file"))
				readTestFile(t, inRoot(root, "c/file"))
			},
		},
		{
			name: "mkdir",
			mutate: func(t *testing.T, r Root) error {
				return r.Mkdir(t.Context(), "/a/b/new", 0o755)
			},
			check: func(t *testing.T, root string) {
				t.Helper()
				requireMissing(t, inRoot(root, "c/new"))
				if info, err := os.Stat(inRoot(root, "a/b.orig/new")); err != nil || !info.IsDir() {
					t.Fatalf("new directory: %v", err)
				}
			},
		},
		{
			name:   "create temp",
			mutate: createTempAndClose,
			check: func(t *testing.T, root string) {
				t.Helper()
				entries, err := os.ReadDir(inRoot(root, "c"))
				if err != nil || len(entries) != 1 {
					t.Fatalf("sibling entries = %v, %v", entries, err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := raceLayout(t)
			swapAfterOpen(t, root, "../c")
			if err := test.mutate(t, Root{Path: root}); err != nil {
				t.Fatal(err)
			}
			test.check(t, root)
		})
	}
}

func createTempAndClose(t *testing.T, r Root) error {
	t.Helper()
	file, _, err := r.CreateTemp(t.Context(), "/a/b", "temp-*")
	if err != nil {
		return err
	}
	return file.Close()
}

func TestRootApplyDeleteRaceLeavesSibling(t *testing.T) {
	root := raceLayout(t)
	swapAfterOpen(t, root, "../c")
	if err := applyDeleteForTest(t, root, "/a/b/file"); err != nil {
		t.Fatal(err)
	}
	requireMissing(t, inRoot(root, "a/b.orig/file"))
	readTestFile(t, inRoot(root, "c/file"))
}

func applyDeleteForTest(t *testing.T, root, logicalPath string) error {
	t.Helper()
	mutation, err := openMutationRoot(root)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()
	return applyDelete(mutation, logicalPath, false)
}

func applyRenameForTest(t *testing.T, root, source, target string) error {
	t.Helper()
	_, err := Root{Path: root}.Apply(t.Context(), Root{Path: t.TempDir()}, acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{Operation: acfstypes.ApplyRename, Path: source, TargetPath: target}},
	})
	return err
}

func TestRenameNoReplaceFallbackNeverOverwrites(t *testing.T) {
	for _, unsupported := range []syscall.Errno{syscall.ENOSYS, syscall.EINVAL} {
		var racer func()
		renameat2 = func(int, string, int, string, uint) error {
			if racer != nil {
				racer()
			}
			return unsupported
		}
		t.Cleanup(func() {
			renameat2 = unix.Renameat2
		})

		root := raceLayout(t)
		if err := applyRenameForTest(t, root, "/a/b/file", "/a/b/moved"); err != nil {
			t.Fatalf("%v: rename file: %v", unsupported, err)
		}
		requireMissing(t, inRoot(root, "a/b/file"))
		if got := readTestFile(t, inRoot(root, "a/b/moved")); got != "original" {
			t.Fatalf("%v: moved = %q", unsupported, got)
		}

		racer = func() {
			writeTestFile(t, inRoot(root, "a/b/late"), "racer")
		}
		err := applyRenameForTest(t, root, "/a/b/moved", "/a/b/late")
		if !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("%v: rename onto concurrent target = %v, want ErrAlreadyExists", unsupported, err)
		}
		if got := readTestFile(t, inRoot(root, "a/b/late")); got != "racer" {
			t.Fatalf("%v: concurrent target = %q", unsupported, got)
		}
		readTestFile(t, inRoot(root, "a/b/moved"))

		racer = nil
		err = applyRenameForTest(t, root, "/a/b", "/a/d")
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("%v: rename directory = %v, want errors.ErrUnsupported", unsupported, err)
		}
		readTestFile(t, inRoot(root, "a/b/moved"))
	}
}

// TestRootErrorShapes pins the *fs.PathError and *os.LinkError details of
// failing mutations to those of v0.7.0.
func TestRootErrorShapes(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"a/b", "a/full/x", "ro"} {
		if err := os.MkdirAll(inRoot(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, inRoot(root, "a/file"), "x")
	writeTestFile(t, inRoot(root, "ro/existing"), "x")
	if err := syscall.Mkfifo(inRoot(root, "a/fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", inRoot(root, "a/filelink")); err != nil {
		t.Fatal(err)
	}
	readOnly := os.Geteuid() != 0
	if readOnly {
		if err := os.Chmod(inRoot(root, "ro"), 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(inRoot(root, "ro"), 0o755)
		})
	}

	r := Root{Path: root}
	ctx := t.Context()
	writeOptions := WriteOptions{Mode: 0o644}
	tests := []struct {
		name     string
		run      func() error
		want     string
		readOnly bool
	}{
		{name: "Remove missing", run: func() error {
			return r.Remove(ctx, "/a/new")
		}, want: `PathError{Op:"removeat" Path:"a/new" Err:0x2}`},
		{name: "Remove through file", run: func() error {
			return r.Remove(ctx, "/a/file/x")
		}, want: `PathError{Op:"removeat" Path:"a/file/x" Err:0x14}`},
		{name: "Remove through link to file", run: func() error {
			return r.Remove(ctx, "/a/filelink/x")
		}, want: `PathError{Op:"removeat" Path:"a/file/x" Err:0x14}`},
		{name: "Remove read-only", run: func() error {
			return r.Remove(ctx, "/ro/existing")
		}, want: `PathError{Op:"removeat" Path:"ro/existing" Err:0xd}`, readOnly: true},
		{name: "RemoveAll through file", run: func() error {
			return r.RemoveAll(ctx, "/a/file/x")
		}, want: `PathError{Op:"RemoveAll" Path:"a/file/x" Err:0x14}`},
		{name: "RemoveAll read-only", run: func() error {
			return r.RemoveAll(ctx, "/ro/existing")
		}, want: `PathError{Op:"RemoveAll" Path:"ro/existing" Err:0xd}`, readOnly: true},
		{name: "Rename missing", run: func() error {
			return r.Rename(ctx, "/a/missing", "/a/new")
		}, want: `LinkError{Op:"renameat" Old:"a/missing" New:"a/new" Err:0x2}`},
		{name: "Rename through file", run: func() error {
			return r.Rename(ctx, "/a/file/x", "/a/new")
		}, want: `LinkError{Op:"renameat" Old:"a/file/x" New:"a/new" Err:0x14}`},
		{name: "Rename into file", run: func() error {
			return r.Rename(ctx, "/a/b", "/a/file/x")
		}, want: `LinkError{Op:"renameat" Old:"a/b" New:"a/file/x" Err:0x14}`},
		{name: "Rename directory onto file", run: func() error {
			return r.Rename(ctx, "/a/b", "/a/file")
		}, want: `LinkError{Op:"renameat" Old:"a/b" New:"a/file" Err:0x14}`},
		{name: "Rename read-only", run: func() error {
			return r.Rename(ctx, "/ro/existing", "/ro/new")
		}, want: `LinkError{Op:"renameat" Old:"ro/existing" New:"ro/new" Err:0xd}`, readOnly: true},
		{name: "Mkdir under file", run: func() error {
			return r.Mkdir(ctx, "/a/file/x", 0o755)
		}, want: `PathError{Op:"mkdirat" Path:"a/file/x" Err:0x14}`},
		{name: "Mkdir read-only", run: func() error {
			return r.Mkdir(ctx, "/ro/new", 0o755)
		}, want: `PathError{Op:"mkdirat" Path:"ro/new" Err:0xd}`, readOnly: true},
		{name: "MkdirAll under file", run: func() error {
			return r.MkdirAll(ctx, "/a/file/x", 0o755)
		}, want: `PathError{Op:"statat" Path:"a/file/x" Err:0x14}`},
		{name: "MkdirAll final file", run: func() error {
			return r.MkdirAll(ctx, "/a/file", 0o755)
		}, want: `PathError{Op:"mkdirat" Path:"a/file" Err:0x11}`},
		{name: "MkdirAll final fifo", run: func() error {
			return r.MkdirAll(ctx, "/a/fifo", 0o755)
		}, want: `PathError{Op:"mkdirat" Path:"a/fifo" Err:0x11}`},
		{name: "MkdirAll final link to file", run: func() error {
			return r.MkdirAll(ctx, "/a/filelink", 0o755)
		}, want: `PathError{Op:"mkdirat" Path:"a/file" Err:0x11}`},
		{name: "MkdirTemp under file", run: func() error {
			_, err := r.MkdirTemp(ctx, "/a/file", "t-*")
			return err
		}, want: `PathError{Op:"mkdirat" Path:"a/file/t-RAND" Err:0x14}`},
		{name: "Write onto directory", run: func() error {
			return r.Write(ctx, "/a/b", []byte("x"), writeOptions)
		}, want: `LinkError{Op:"renameat" Old:"a/.acfs-write-RAND" New:"a/b" Err:0x11}`},
		{name: "Write under file", run: func() error {
			return r.Write(ctx, "/a/file/x", []byte("x"), writeOptions)
		}, want: `PathError{Op:"openat" Path:"a/file/.acfs-write-RAND" Err:0x14}`},
		{name: "Write in place under file", run: func() error {
			return r.Write(ctx, "/a/file/x", []byte("x"), WriteOptions{Mode: 0o644, InPlace: true})
		}, want: `PathError{Op:"statat" Path:"a/file/x" Err:0x14}`},
		{name: "WriteAt directory", run: func() error {
			return r.WriteAt(ctx, "/a/b", 0, []byte("x"))
		}, want: `PathError{Op:"openat" Path:"a/b" Err:0x15}`},
		{name: "WriteAt under file", run: func() error {
			return r.WriteAt(ctx, "/a/file/x", 0, []byte("x"))
		}, want: `PathError{Op:"statat" Path:"a/file/x" Err:0x14}`},
	}
	randomName := regexp.MustCompile(`(\.acfs-write-[0-9a-f]{32}|t-[0-9a-f]{16})`)
	for _, test := range tests {
		if test.readOnly && !readOnly {
			continue
		}
		err := test.run()
		var got string
		var pathErr *fs.PathError
		var linkErr *os.LinkError
		switch {
		case errors.As(err, &linkErr):
			got = fmt.Sprintf("LinkError{Op:%q Old:%q New:%q Err:%#v}", linkErr.Op, linkErr.Old, linkErr.New, linkErr.Err)
		case errors.As(err, &pathErr):
			got = fmt.Sprintf("PathError{Op:%q Path:%q Err:%#v}", pathErr.Op, pathErr.Path, pathErr.Err)
		default:
			t.Errorf("%s: error = %v, want a path or link error", test.name, err)
			continue
		}
		got = randomName.ReplaceAllStringFunc(got, func(name string) string {
			return name[:strings.LastIndexAny(name, "-")+1] + "RAND"
		})
		if got != test.want {
			t.Errorf("%s:\n got %s\nwant %s", test.name, got, test.want)
		}
	}
	if err := r.MkdirAll(ctx, "/a/fifo", 0o755); !errors.Is(err, fs.ErrExist) {
		t.Errorf("MkdirAll over fifo = %v, want fs.ErrExist", err)
	}
}

func TestErrorsArePinned(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := Root{Path: root}
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "a", "file"), "x", 0o644)
	for _, link := range [][2]string{{"missing", "dangling"}, {"/etc", "absolute"}, {"loop", "loop"}} {
		if err := os.Symlink(link[0], filepath.Join(root, link[1])); err != nil {
			t.Fatal(err)
		}
	}
	permissionCases := os.Geteuid() != 0
	if permissionCases {
		for _, directory := range []string{"no-search", filepath.Join("no-read", "d")} {
			if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, filepath.Join(root, directory, "f"), "x", 0o644)
		}
		if err := os.Chmod(filepath.Join(root, "no-search"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, "no-read"), 0o300); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(filepath.Join(root, "no-search"), 0o755)
			_ = os.Chmod(filepath.Join(root, "no-read"), 0o755)
		})
	}

	ctx := t.Context()
	cases := []struct {
		run        func() error
		name       string
		want       string
		permission bool
	}{
		{name: "list file", run: func() error {
			_, err := workspace.List(ctx, "/a/file")
			return err
		}, want: `read directory "/a/file": readdirent <root>/a/file: not a directory`},
		{name: "list missing", run: func() error {
			_, err := workspace.List(ctx, "/missing")
			return err
		}, want: `lstat "/missing": statat missing: no such file or directory`},
		{
			name: "list no-search",
			run: func() error {
				_, err := workspace.List(ctx, "/no-search")
				return err
			},
			want:       `stat "/no-search/f": statat no-search/f: permission denied`,
			permission: true,
		},
		{name: "list no-read", run: func() error {
			_, err := workspace.List(ctx, "/no-read")
			return err
		}, want: `open directory "/no-read": openat no-read: permission denied`, permission: true},
		{
			name: "stat through file",
			run: func() error {
				_, err := workspace.Stat(ctx, "/a/file/c", acfstypes.StatOptions{})
				return err
			},
			want: `lstat "/a/file/c": statat a/file/c: not a directory`,
		},
		{
			name: "stat missing parent",
			run: func() error {
				_, err := workspace.Stat(ctx, "/a/missing/c", acfstypes.StatOptions{})
				return err
			},
			want: `lstat "/a/missing": statat a/missing: no such file or directory`,
		},
		{
			name: "stat missing final",
			run: func() error {
				_, err := workspace.Stat(ctx, "/a/b/missing", acfstypes.StatOptions{})
				return err
			},
			want: `lstat "/a/b/missing": statat a/b/missing: no such file or directory`,
		},
		{
			name: "stat no-search child",
			run: func() error {
				_, err := workspace.Stat(ctx, "/no-search/f", acfstypes.StatOptions{})
				return err
			},
			want:       `lstat "/no-search/f": statat no-search/f: permission denied`,
			permission: true,
		},
		{
			name: "stat dangling followed",
			run: func() error {
				_, err := workspace.Stat(ctx, "/dangling", acfstypes.StatOptions{FollowFinal: true})
				return err
			},
			want: `lstat "/missing": statat missing: no such file or directory`,
		},
		{
			name: "stat absolute link",
			run: func() error {
				_, err := workspace.Stat(ctx, "/absolute/x", acfstypes.StatOptions{})
				return err
			},
			want: `path escapes workspace root: symlink "/absolute" targets "/etc"`,
		},
		{name: "stat loop", run: func() error {
			_, err := workspace.Stat(ctx, "/loop", acfstypes.StatOptions{FollowFinal: true})
			return err
		}, want: `too many symbolic links`},
		{name: "read directory", run: func() error {
			_, err := workspace.ReadFile(ctx, "/a")
			return err
		}, want: `path is a directory`},
		{name: "mkdir under file", run: func() error {
			return workspace.MkdirAll(ctx, "/a/file/x", 0o755)
		}, want: `lstat "/a/file/x": statat a/file/x: not a directory`},
		{
			name: "mkdir under no-search",
			run: func() error {
				return workspace.MkdirAll(ctx, "/no-search/x/y", 0o755)
			},
			want:       `lstat "/no-search/x": statat no-search/x: permission denied`,
			permission: true,
		},
	}
	for _, testCase := range cases {
		if testCase.permission && !permissionCases {
			continue
		}
		err := testCase.run()
		if err == nil {
			t.Errorf("%s: error = nil, want %q", testCase.name, testCase.want)
			continue
		}
		if got := strings.ReplaceAll(err.Error(), root, "<root>"); got != testCase.want {
			t.Errorf("%s: error = %q, want %q", testCase.name, got, testCase.want)
		}
	}

	if _, err := workspace.Stat(ctx, "/a/file/c", acfstypes.StatOptions{}); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("Stat through file = %v, want ENOTDIR", err)
	}
	var pathErr *fs.PathError
	if _, err := workspace.Stat(ctx, "/a/b/missing", acfstypes.StatOptions{}); !errors.As(err, &pathErr) || pathErr.Op != "statat" || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat missing = %#v, want statat PathError wrapping ErrNotExist", err)
	}
}
