package acfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRootSymlink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want       error
		name       string
		target     string
		link       string
		wantStored string
	}{
		{name: "relative target", target: "file", link: "/rel", wantStored: "file"},
		{name: "relative target in subdirectory", target: "../file", link: "/dir/up", wantStored: "../file"},
		{name: "missing target inside root", target: "dir/later", link: "/later", wantStored: "dir/later"},
		{name: "absolute logical target", target: "/dir/inner", link: "/abs", wantStored: "dir/inner"},
		{name: "absolute logical target from subdirectory", target: "/file", link: "/dir/abs", wantStored: "../file"},
		{name: "absolute root target", target: "/", link: "/dir/top", wantStored: ".."},
		{name: "absolute target with dot-dot after directory", target: "/dir/../file", link: "/dotdot", wantStored: "file"},
		{name: "relative escape", target: "../outside", link: "/out", want: ErrOutsideRoot},
		{name: "relative escape from subdirectory", target: "../../outside", link: "/dir/out", want: ErrOutsideRoot},
		{name: "escape through existing link", target: "escape/secret", link: "/through", want: ErrOutsideRoot},
		{name: "escape after missing component", target: "parent/later/../../secret", link: "/dir/new", want: ErrOutsideRoot},
		{name: "reserved target", target: temporaryWritePrefix + "x", link: "/reserved", want: ErrInvalidPath},
		{name: "empty target", link: "/empty", want: ErrInvalidPath},
		{name: "symlinked parent", target: "inner", link: "/dir-link/new", want: ErrSymlink},
		{name: "reserved link path", target: "file", link: "/" + temporaryWritePrefix + "x", want: ErrInvalidPath},
		{name: "link path escape", target: "file", link: "/../x", want: ErrInvalidPath},
		{name: "existing link path", target: "file", link: "/file-link", want: ErrAlreadyExists},
		{name: "missing parent", target: "file", link: "/missing/link", want: fs.ErrNotExist},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir, _ := newSymlinkFixture(t)
			root := Root{Path: dir}
			err := root.Symlink(t.Context(), test.target, test.link)
			assertError(t, "Symlink", err, test.want)
			if test.want != nil {
				return
			}
			stored, err := os.Readlink(filepath.Join(dir, test.link))
			if err != nil {
				t.Fatal(err)
			}
			if stored != test.wantStored {
				t.Fatalf("stored target = %q, want %q", stored, test.wantStored)
			}
			got, err := root.Readlink(t.Context(), test.link)
			if err != nil || got != test.wantStored {
				t.Fatalf("Readlink = %q, %v; want %q", got, err, test.wantStored)
			}
		})
	}
}

func TestRootSymlinkRejectsTargetsResolvedDifferentlyOnHost(t *testing.T) {
	t.Parallel()

	dir, _ := newSymlinkFixture(t)
	for _, directory := range []string{"a", "b/sub"} {
		if err := os.MkdirAll(filepath.Join(dir, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"host-absolute": filepath.Join(dir, "dir"),
		"volume":        "/volume/dir",
		"loop-a":        "loop-b",
		"loop-b":        "loop-a",
		"a/l":           "../b/sub",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		want   error
		target string
	}{
		{target: "host-absolute/inner", want: ErrOutsideRoot},
		{target: "/host-absolute/inner", want: ErrOutsideRoot},
		{target: "volume/inner", want: ErrOutsideRoot},
		{target: "loop-a/inner", want: ErrSymlinkLoop},
		{target: "/a/l/../file", want: ErrInvalidPath},
		{target: "/a/l/../../file", want: ErrInvalidPath},
	}
	for index, test := range tests {
		link := fmt.Sprintf("/new-%d", index)
		err := Root{Path: dir}.Symlink(t.Context(), test.target, link)
		assertError(t, test.target, err, test.want)
		if _, lstatErr := os.Lstat(filepath.Join(dir, link)); !errors.Is(lstatErr, fs.ErrNotExist) {
			t.Fatalf("%s: link was created or lstat failed: %v", test.target, lstatErr)
		}
	}
}

func TestRootSymlinkRejectsWindowsTargets(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("native separators and volume names only exist on Windows")
	}

	dir, _ := newSymlinkFixture(t)
	for target, want := range map[string]error{
		`dir\inner`:        ErrInvalidPath,
		`C:\file`:          ErrInvalidPath,
		"C:file":           ErrOutsideRoot,
		"C:/file":          ErrOutsideRoot,
		"//server/share/x": ErrOutsideRoot,
	} {
		err := Root{Path: dir}.Symlink(t.Context(), target, "/link")
		assertError(t, target, err, want)
	}
}

func TestRootSymlinkExistingReportsExist(t *testing.T) {
	t.Parallel()

	dir, _ := newSymlinkFixture(t)
	err := Root{Path: dir}.Symlink(t.Context(), "file", "/file")
	if !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Symlink over file error = %v, want ErrAlreadyExists and fs.ErrExist", err)
	}
}

func TestHostSymlink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want   error
		name   string
		target string
		link   string
	}{
		{name: "relative target", target: "file", link: "rel"},
		{name: "absolute target outside", target: "/etc/hostname", link: "abs"},
		{name: "through symlinked parent", target: "x", link: "dir-link/new"},
		{name: "existing link", target: "file", link: "file-link", want: ErrAlreadyExists},
		{name: "empty target", link: "empty", want: ErrInvalidPath},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir, _ := newSymlinkFixture(t)
			link := filepath.Join(dir, test.link)
			err := Host{}.Symlink(t.Context(), test.target, link)
			assertError(t, "Symlink", err, test.want)
			if test.want != nil {
				return
			}
			got, err := Host{}.Readlink(t.Context(), link)
			if err != nil || got != test.target {
				t.Fatalf("Readlink = %q, %v; want %q", got, err, test.target)
			}
		})
	}

	if err := (Host{}).Symlink(t.Context(), "file", "relative"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("relative link path error = %v, want ErrInvalidPath", err)
	}
}

func TestReadlink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantRoot error
		wantHost error
		target   func(outside string) string
		name     string
		path     string
	}{
		{name: "internal link", path: "/file-link", target: func(string) string {
			return "file"
		}},
		{name: "external link is verbatim", path: "/escape", target: func(outside string) string {
			return outside
		}},
		{name: "through symlinked parent", path: "/dir-link/inner", wantRoot: errAnyFailure, wantHost: errAnyFailure},
		{name: "not a link", path: "/file", wantRoot: errAnyFailure, wantHost: errAnyFailure},
		{name: "missing", path: "/missing", wantRoot: fs.ErrNotExist, wantHost: fs.ErrNotExist},
		{name: "outside root through parent link", path: "/escape/secret", wantRoot: ErrOutsideRoot, wantHost: errAnyFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for index := range testFilesystems("") {
				dir, outside := newSymlinkFixture(t)
				filesystem := testFilesystems(dir)[index]
				want := test.wantRoot
				if filesystem.name == "host" {
					want = test.wantHost
				}
				got, err := filesystem.fs.Readlink(t.Context(), filesystem.path(test.path))
				assertError(t, filesystem.name, err, want)
				if want == nil && got != test.target(outside) {
					t.Fatalf("%s: Readlink = %q, want %q", filesystem.name, got, test.target(outside))
				}
			}
		})
	}
}
