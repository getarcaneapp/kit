package acfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMkdirTempCreatesUniqueDirectoriesFromPattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()

	first, err := Root{Path: root}.MkdirTemp(ctx, "/", "stage-*.tmp")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	second, err := Root{Path: root}.MkdirTemp(ctx, "/", "stage-*.tmp")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if first == second {
		t.Fatalf("MkdirTemp returned duplicate path %q", first)
	}

	name := strings.TrimPrefix(first, "/")
	if !strings.HasPrefix(name, "stage-") || !strings.HasSuffix(name, ".tmp") {
		t.Fatalf("temporary name %q does not follow the pattern", name)
	}
	info, err := os.Stat(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != temporaryDirectoryMode {
		t.Fatalf("temporary directory mode = %v, want %v", info.Mode(), os.FileMode(temporaryDirectoryMode))
	}

	_, err = Root{Path: root}.MkdirTemp(ctx, "/", "nested/pattern")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("MkdirTemp with multi-component pattern error = %v, want ErrInvalidPath", err)
	}
}

func TestCreateTemp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want    error
		name    string
		dir     string
		pattern string
		prefix  string
		suffix  string
	}{
		{name: "suffix pattern", dir: "/dir", pattern: "lock-*.tmp", prefix: "/dir/lock-", suffix: ".tmp"},
		{name: "prefix pattern", dir: "/", pattern: "scan", prefix: "/scan"},
		{name: "through internal link", dir: "/dir-link", pattern: "x-*", prefix: "/dir/x-"},
		{name: "multi-component pattern", dir: "/", pattern: "a/b", want: ErrInvalidPath},
		{name: "reserved pattern", dir: "/", pattern: temporaryWritePrefix + "*", want: ErrInvalidPath},
		{name: "missing directory", dir: "/missing", pattern: "x", want: fs.ErrNotExist},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for index := range testFilesystems("") {
				dir, _ := newSymlinkFixture(t)
				filesystem := testFilesystems(dir)[index]
				file, created, err := filesystem.fs.CreateTemp(t.Context(), filesystem.path(test.dir), test.pattern)
				assertError(t, filesystem.name, err, test.want)
				if test.want != nil {
					continue
				}
				_ = file.Close()
				logical := normalizeEntryPath(dir, created)
				if filesystem.name == "host" && test.name == "through internal link" {
					logical = strings.Replace(logical, "/dir-link/", "/dir/", 1)
				}
				if !strings.HasPrefix(logical, test.prefix) || !strings.HasSuffix(logical, test.suffix) || logical == test.prefix+test.suffix {
					t.Fatalf("%s: created %q, want %q*%q", filesystem.name, created, test.prefix, test.suffix)
				}
				info, err := os.Lstat(filepath.Join(dir, logical))
				if err != nil {
					t.Fatalf("%s: %v", filesystem.name, err)
				}
				if !info.Mode().IsRegular() || info.Mode().Perm() != temporaryFileMode {
					t.Fatalf("%s: mode = %v, want regular %v", filesystem.name, info.Mode(), os.FileMode(temporaryFileMode))
				}
			}
		})
	}
}

func TestCreateTempRejectsEscapingDirectory(t *testing.T) {
	t.Parallel()

	dir, _ := newSymlinkFixture(t)
	_, _, err := Root{Path: dir}.CreateTemp(t.Context(), "/escape", "x")
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("CreateTemp through escaping link error = %v, want ErrOutsideRoot", err)
	}
	_, _, err = Host{}.CreateTemp(t.Context(), "relative", "x")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Host CreateTemp relative error = %v, want ErrInvalidPath", err)
	}
}

func TestTemporaryPatternsCannotLeaveDirectory(t *testing.T) {
	t.Parallel()

	patterns := []string{"x/y-*", "x" + string(filepath.Separator) + "y-*", "../y-*"}
	if runtime.GOOS == "windows" {
		patterns = append(patterns, "C:y-*", `\\server\share\y-*`)
	}
	for _, filesystem := range testFilesystems(t.TempDir()) {
		for _, pattern := range patterns {
			if _, err := filesystem.fs.MkdirTemp(t.Context(), filesystem.path("/"), pattern); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("%s MkdirTemp(%q) error = %v, want ErrInvalidPath", filesystem.name, pattern, err)
			}
			file, _, err := filesystem.fs.CreateTemp(t.Context(), filesystem.path("/"), pattern)
			if !errors.Is(err, ErrInvalidPath) {
				t.Errorf("%s CreateTemp(%q) error = %v, want ErrInvalidPath", filesystem.name, pattern, err)
			}
			if file != nil {
				_ = file.Close()
			}
		}
	}
}
