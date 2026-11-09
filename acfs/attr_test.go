package acfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// errAnyFailure marks a test expectation that accepts any non-nil error.
var errAnyFailure = errors.New("any failure")

// newSymlinkFixture builds a tree with a file, a directory, internal symbolic
// links, and a link escaping to a directory outside it.
func newSymlinkFixture(t *testing.T) (dir, outside string) {
	t.Helper()
	dir = t.TempDir()
	outside = t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, "file"), "data", 0o644)
	writeFixtureFile(t, filepath.Join(outside, "secret"), "secret", 0o644)
	if err := os.MkdirAll(filepath.Join(dir, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(dir, "dir", "inner"), "inner", 0o644)
	for link, target := range map[string]string{
		"file-link":  "file",
		"dir-link":   "dir",
		"escape":     outside,
		"dir/parent": "..",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	return dir, outside
}

func TestAttributeChanges(t *testing.T) {
	t.Parallel()

	uid, gid := os.Getuid(), os.Getgid()
	stamp := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		wantRoot error
		wantHost error
		change   func(t *testing.T, fsys FS, target string) error
		check    func(t *testing.T, filename string)
		name     string
		path     string
	}{
		{
			name: "chmod file",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, 0o600)
			},
			path:  "/file",
			check: expectMode(0o600),
		},
		{
			name: "chmod directory",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, 0o700|os.ModeSticky)
			},
			path:  "/dir",
			check: expectMode(0o700 | os.ModeSticky),
		},
		{
			name: "chmod invalid mode",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, os.ModeDir|0o755)
			},
			path:     "/file",
			wantRoot: errAnyFailure,
			wantHost: errAnyFailure,
		},
		{
			name: "chmod final symlink",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, 0o600)
			},
			path:     "/file-link",
			wantRoot: ErrSymlink,
		},
		{
			name: "chmod through symlinked parent",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, 0o600)
			},
			path:     "/dir-link/inner",
			wantRoot: ErrSymlink,
		},
		{
			name: "chmod escaping link",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, 0o700)
			},
			path:     "/escape/secret",
			wantRoot: ErrSymlink,
		},
		{
			name: "chmod missing",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, 0o600)
			},
			path:     "/missing",
			wantRoot: fs.ErrNotExist,
			wantHost: fs.ErrNotExist,
		},
		{
			name: "chmod reserved temporary path",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chmod(t.Context(), target, 0o600)
			},
			path:     "/" + temporaryWritePrefix + "x",
			wantRoot: ErrInvalidPath,
			wantHost: ErrInvalidPath,
		},
		{
			name: "chown file",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chown(t.Context(), target, uid, gid)
			},
			path: "/file",
		},
		{
			name: "chown final symlink",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chown(t.Context(), target, uid, -1)
			},
			path:     "/file-link",
			wantRoot: ErrSymlink,
		},
		{
			name: "chown through symlinked parent",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chown(t.Context(), target, -1, gid)
			},
			path:     "/dir-link/inner",
			wantRoot: ErrSymlink,
		},
		{
			name: "lchown final symlink",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Lchown(t.Context(), target, uid, gid)
			},
			path: "/file-link",
		},
		{
			name: "lchown through symlinked parent",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Lchown(t.Context(), target, uid, gid)
			},
			path:     "/dir-link/inner",
			wantRoot: ErrSymlink,
		},
		{
			name: "chtimes file",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chtimes(t.Context(), target, stamp, stamp)
			},
			path:  "/file",
			check: expectModTime(stamp),
		},
		{
			name: "chtimes root directory",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chtimes(t.Context(), target, stamp, stamp)
			},
			path:  "/",
			check: expectModTime(stamp),
		},
		{
			name: "chtimes final symlink",
			change: func(t *testing.T, fsys FS, target string) error {
				return fsys.Chtimes(t.Context(), target, stamp, stamp)
			},
			path:     "/file-link",
			wantRoot: ErrSymlink,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for index := range testFilesystems("") {
				dir, _ := newSymlinkFixture(t)
				filesystem := testFilesystems(dir)[index]
				want := test.wantRoot
				if filesystem.name == "host" {
					want = test.wantHost
				}
				err := test.change(t, filesystem.fs, filesystem.path(test.path))
				assertError(t, filesystem.name, err, want)
				if want == nil && test.check != nil {
					test.check(t, filepath.Join(dir, test.path))
				}
			}
		})
	}
}

func TestHostAttributeChangesRejectRelativePaths(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	for name, err := range map[string]error{
		"chmod":   Host{}.Chmod(ctx, "relative", 0o600),
		"chown":   Host{}.Chown(ctx, "", -1, -1),
		"lchown":  Host{}.Lchown(ctx, "a\x00b", -1, -1),
		"chtimes": Host{}.Chtimes(ctx, "../x", time.Time{}, time.Time{}),
	} {
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s error = %v, want ErrInvalidPath", name, err)
		}
	}
}

func TestRootAttributeChangesRejectEscapes(t *testing.T) {
	t.Parallel()

	dir, _ := newSymlinkFixture(t)
	root := Root{Path: dir}
	err := root.Chmod(t.Context(), "/../outside", 0o600)
	if !errors.Is(err, ErrInvalidPath) && !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Chmod escape error = %v, want ErrInvalidPath or ErrOutsideRoot", err)
	}
}

// assertError fails unless err matches want; a nil want requires success.
func assertError(t *testing.T, label string, err, want error) {
	t.Helper()
	switch {
	case want == nil:
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", label, err)
		}
	case errors.Is(want, errAnyFailure):
		if err == nil {
			t.Fatalf("%s: expected an error", label)
		}
	case !errors.Is(err, want):
		t.Fatalf("%s: error = %v, want %v", label, err, want)
	}
}

func expectMode(want os.FileMode) func(t *testing.T, filename string) {
	return func(t *testing.T, filename string) {
		t.Helper()
		info, err := os.Lstat(filename)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode() & chmodModeMask; got != want {
			t.Fatalf("mode = %v, want %v", got, want)
		}
	}
}

func expectModTime(want time.Time) func(t *testing.T, filename string) {
	return func(t *testing.T, filename string) {
		t.Helper()
		info, err := os.Lstat(filename)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(want) {
			t.Fatalf("mtime = %v, want %v", info.ModTime(), want)
		}
	}
}
