package acfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	acfstypes "kit.getarcane.app/acfs/types"
)

type (
	// testFS pairs an FS with the mapping from a logical path under its directory
	// to the path that FS expects.
	testFS struct {
		fs   FS
		path func(logicalPath string) string
		name string
	}
)

var conformanceSentinels = []error{
	ErrInvalidPath, ErrOutsideRoot, ErrIsDirectory, ErrNotFile, ErrNotDirectory, ErrNotEmpty,
	ErrAlreadyExists, ErrSymlink, ErrRootRemoval, ErrSizeMismatch, fs.ErrNotExist, fs.ErrExist,
}

// testFilesystems returns a Root and a Host over dir.
func testFilesystems(dir string) []testFS {
	return []testFS{
		{
			name: "root",
			fs:   Root{Path: dir},
			path: func(logicalPath string) string {
				return logicalPath
			},
		},
		{
			name: "host",
			fs:   Host{},
			path: func(logicalPath string) string {
				return filepath.Join(dir, logicalPath)
			},
		},
	}
}

// classifyError renders the sentinels err matches, so Root and Host errors can
// be compared without their path-specific messages.
func classifyError(err error) string {
	if err == nil {
		return "ok"
	}
	var matched []string
	for _, sentinel := range conformanceSentinels {
		if errors.Is(err, sentinel) {
			matched = append(matched, sentinel.Error())
		}
	}
	if len(matched) == 0 {
		return "error"
	}
	return strings.Join(matched, "|")
}

// normalizeEntryPath maps a Host entry path back to its logical path.
func normalizeEntryPath(dir, entryPath string) string {
	for _, prefix := range []string{dir, mustEvalSymlinks(dir)} {
		if entryPath == prefix {
			return "/"
		}
		if trimmed, ok := strings.CutPrefix(entryPath, prefix+"/"); ok {
			return "/" + trimmed
		}
	}
	return entryPath
}

func mustEvalSymlinks(dir string) string {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

func describeEntry(dir string, entry acfstypes.Entry) string {
	return fmt.Sprintf("%s path=%s mode=%s size=%d dir=%t link=%t target=%q",
		entry.Name, normalizeEntryPath(dir, entry.Path), entry.Mode, entry.Size, entry.IsDirectory, entry.IsSymlink, entry.LinkTarget)
}

// runConformanceScript applies the same operation sequence through filesystem
// and records each normalized result.
func runConformanceScript(t *testing.T, dir string, filesystem testFS) []string {
	t.Helper()
	ctx := t.Context()
	var results []string
	record := func(step, result string, err error) {
		results = append(results, fmt.Sprintf("%s: %s [%s]", step, result, classifyError(err)))
	}
	entries := func(list []acfstypes.Entry) string {
		described := make([]string, 0, len(list))
		for _, entry := range list {
			described = append(described, describeEntry(dir, entry))
		}
		return strings.Join(described, "; ")
	}

	record("mkdirall", "", filesystem.fs.MkdirAll(ctx, filesystem.path("/a/b"), 0o755))
	record("mkdirall-existing", "", filesystem.fs.MkdirAll(ctx, filesystem.path("/a/b"), 0o755))
	record("mkdirall-under-file", "", errors.Join(
		filesystem.fs.Write(ctx, filesystem.path("/plain"), []byte("x"), WriteOptions{Mode: 0o600}),
		filesystem.fs.MkdirAll(ctx, filesystem.path("/plain/sub"), 0o755),
	))
	record("write", "", filesystem.fs.Write(ctx, filesystem.path("/a/b/file"), []byte("hello"), WriteOptions{Mode: 0o640}))
	record("write-in-place", "", filesystem.fs.Write(ctx, filesystem.path("/a/b/file"), []byte("world!"), WriteOptions{Mode: 0o600, InPlace: true}))
	record("write-directory", "", filesystem.fs.Write(ctx, filesystem.path("/a"), []byte("x"), WriteOptions{Mode: 0o600}))
	record("write-missing-parent", "", filesystem.fs.Write(ctx, filesystem.path("/missing/file"), []byte("x"), WriteOptions{Mode: 0o600}))
	record("write-reserved", "", filesystem.fs.Write(ctx, filesystem.path("/"+temporaryWritePrefix+"x"), []byte("x"), WriteOptions{Mode: 0o600}))

	content, err := filesystem.fs.ReadFile(ctx, filesystem.path("/a/b/file"))
	record("readfile", string(content), err)
	_, err = filesystem.fs.ReadFile(ctx, filesystem.path("/a"))
	record("readfile-directory", "", err)
	_, err = filesystem.fs.ReadFile(ctx, filesystem.path("/missing"))
	record("readfile-missing", "", err)

	entry, err := filesystem.fs.Stat(ctx, filesystem.path("/a/b/file"), acfstypes.StatOptions{})
	record("stat", describeEntry(dir, entry), err)
	_, err = filesystem.fs.Stat(ctx, filesystem.path("/missing"), acfstypes.StatOptions{})
	record("stat-missing", "", err)

	record("symlink", "", errors.Join(os.Symlink("b/file", filepath.Join(dir, "a", "link"))))
	entry, err = filesystem.fs.Stat(ctx, filesystem.path("/a/link"), acfstypes.StatOptions{})
	record("stat-link", describeEntry(dir, entry), err)
	entry, err = filesystem.fs.Stat(ctx, filesystem.path("/a/link"), acfstypes.StatOptions{FollowFinal: true})
	record("stat-link-follow", describeEntry(dir, entry), err)
	content, err = filesystem.fs.ReadFile(ctx, filesystem.path("/a/link"))
	record("readfile-link", string(content), err)

	list, err := filesystem.fs.List(ctx, filesystem.path("/a"))
	record("list", entries(list), err)
	_, err = filesystem.fs.List(ctx, filesystem.path("/missing"))
	record("list-missing", "", err)

	exists, err := filesystem.fs.Exists(ctx, filesystem.path("/a/link"))
	record("exists", strconv.FormatBool(exists), err)
	exists, err = filesystem.fs.Exists(ctx, filesystem.path("/missing"))
	record("exists-missing", strconv.FormatBool(exists), err)

	record("mkdir-existing", "", filesystem.fs.Mkdir(ctx, filesystem.path("/a"), 0o755))
	record("rename", "", filesystem.fs.Rename(ctx, filesystem.path("/a/b/file"), filesystem.path("/a/moved")))
	record("rename-into-self", "", filesystem.fs.Rename(ctx, filesystem.path("/a"), filesystem.path("/a/b/c")))
	record("rename-missing", "", filesystem.fs.Rename(ctx, filesystem.path("/missing"), filesystem.path("/a/other")))
	record("mkdir", "", filesystem.fs.Mkdir(ctx, filesystem.path("/full"), 0o755))
	record("write-full", "", filesystem.fs.Write(ctx, filesystem.path("/full/file"), []byte("x"), WriteOptions{Mode: 0o600}))
	record("rename-onto-non-empty", "", filesystem.fs.Rename(ctx, filesystem.path("/a/b"), filesystem.path("/full")))
	record("remove-non-empty", "", filesystem.fs.Remove(ctx, filesystem.path("/a")))
	record("remove-missing", "", filesystem.fs.Remove(ctx, filesystem.path("/missing")))
	record("remove-link", "", filesystem.fs.Remove(ctx, filesystem.path("/a/link")))
	record("remove-empty", "", filesystem.fs.Remove(ctx, filesystem.path("/a/b")))
	record("removeall-missing", "", filesystem.fs.RemoveAll(ctx, filesystem.path("/missing/child")))
	record("removeall", "", filesystem.fs.RemoveAll(ctx, filesystem.path("/a")))
	return results
}

func TestRootAndHostConform(t *testing.T) {
	t.Parallel()

	var expected []string
	for index := range testFilesystems("") {
		dir := t.TempDir()
		filesystem := testFilesystems(dir)[index]
		results := runConformanceScript(t, dir, filesystem)
		if expected == nil {
			expected = results
			continue
		}
		for step := range max(len(expected), len(results)) {
			var want, got string
			if step < len(expected) {
				want = expected[step]
			}
			if step < len(results) {
				got = results[step]
			}
			if want != got {
				t.Errorf("%s diverges from root:\n got %s\nwant %s", filesystem.name, got, want)
			}
		}
	}
}

func TestExclusiveWrite(t *testing.T) {
	t.Parallel()

	for index, named := range testFilesystems("") {
		t.Run(named.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			filesystem := testFilesystems(dir)[index]
			ctx := t.Context()

			err := filesystem.fs.Write(ctx, filesystem.path("/config"), []byte("first"), WriteOptions{Mode: 0o600, Exclusive: true})
			if err != nil {
				t.Fatalf("Write absent: %v", err)
			}
			err = filesystem.fs.Write(ctx, filesystem.path("/config"), []byte("second"), WriteOptions{Mode: 0o600, Exclusive: true})
			if !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, fs.ErrExist) {
				t.Fatalf("Write existing = %v, want ErrAlreadyExists and fs.ErrExist", err)
			}
			_, err = filesystem.fs.WriteFrom(ctx, filesystem.path("/config"), strings.NewReader("third"), WriteFromOptions{Size: 5, Mode: 0o600, Exclusive: true})
			if !errors.Is(err, ErrAlreadyExists) || !errors.Is(err, fs.ErrExist) {
				t.Fatalf("WriteFrom existing = %v, want ErrAlreadyExists and fs.ErrExist", err)
			}
			_, err = filesystem.fs.WriteFrom(ctx, filesystem.path("/streamed"), strings.NewReader("data"), WriteFromOptions{Size: 4, Mode: 0o600, Exclusive: true})
			if err != nil {
				t.Fatalf("WriteFrom absent: %v", err)
			}
			err = filesystem.fs.Write(ctx, filesystem.path("/other"), []byte("x"), WriteOptions{Mode: 0o600, Exclusive: true, InPlace: true})
			if !errors.Is(err, ErrInvalidWriteOptions) {
				t.Fatalf("Write Exclusive+InPlace = %v, want ErrInvalidWriteOptions", err)
			}

			content, err := os.ReadFile(filepath.Join(dir, "config"))
			if err != nil || string(content) != "first" {
				t.Fatalf("config = %q (%v), want first", content, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 {
				t.Fatalf("directory holds %d entries, want config and streamed only", len(entries))
			}
		})
	}
}
