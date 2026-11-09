//go:build unix

package acfs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOpenFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantRoot    error
		wantHost    error
		name        string
		path        string
		write       string
		wantContent string
		options     OpenOptions
	}{
		{name: "read existing", path: "/file", options: OpenOptions{Read: true}, wantContent: "data"},
		{name: "read through internal link", path: "/file-link", options: OpenOptions{Read: true}, wantContent: "data"},
		{name: "read through symlinked parent", path: "/dir-link/inner", options: OpenOptions{Read: true}, wantContent: "inner"},
		{
			name: "create exclusive", path: "/new", options: OpenOptions{Write: true, Create: true, Exclusive: true, Mode: 0o600},
			write: "fresh", wantContent: "fresh",
		},
		{
			name: "create exclusive existing", path: "/file", options: OpenOptions{Write: true, Create: true, Exclusive: true, Mode: 0o600},
			wantRoot: ErrAlreadyExists, wantHost: ErrAlreadyExists,
		},
		{name: "append", path: "/file", options: OpenOptions{Write: true, Append: true}, write: "+more", wantContent: "data+more"},
		{name: "truncate", path: "/file", options: OpenOptions{Write: true, Truncate: true}, write: "new", wantContent: "new"},
		{name: "read write", path: "/file", options: OpenOptions{Read: true, Write: true}, write: "DA", wantContent: "DAta"},
		{
			name: "create existing keeps content", path: "/file", options: OpenOptions{Read: true, Write: true, Create: true, Mode: 0o600},
			wantContent: "data",
		},
		{name: "write missing without create", path: "/missing", options: OpenOptions{Write: true}, wantRoot: fs.ErrNotExist, wantHost: fs.ErrNotExist},
		{name: "read missing", path: "/missing", options: OpenOptions{Read: true}, wantRoot: fs.ErrNotExist, wantHost: fs.ErrNotExist},
		{name: "create missing parent", path: "/missing/file", options: OpenOptions{Write: true, Create: true}, wantRoot: fs.ErrNotExist, wantHost: fs.ErrNotExist},
		{name: "directory", path: "/dir", options: OpenOptions{Read: true}, wantRoot: ErrIsDirectory, wantHost: ErrIsDirectory},
		{name: "root directory", path: "/", options: OpenOptions{Read: true}, wantRoot: ErrIsDirectory, wantHost: ErrIsDirectory},
		{name: "fifo", path: "/fifo", options: OpenOptions{Write: true}, wantRoot: ErrNotFile, wantHost: ErrNotFile},
		{name: "write final symlink", path: "/file-link", options: OpenOptions{Write: true}, wantRoot: ErrSymlink, write: "x", wantContent: "x"},
		{name: "create final symlink", path: "/file-link", options: OpenOptions{Write: true, Create: true}, wantRoot: ErrSymlink, write: "x", wantContent: "x"},
		{name: "write through symlinked parent", path: "/dir-link/inner", options: OpenOptions{Write: true}, wantRoot: ErrSymlink, write: "x", wantContent: "xnner"},
		{name: "read escaping link", path: "/escape/secret", options: OpenOptions{Read: true}, wantRoot: ErrOutsideRoot, wantContent: "secret"},
		{name: "write escaping link", path: "/escape/secret", options: OpenOptions{Write: true}, wantRoot: ErrSymlink, write: "S", wantContent: "Secret"},
		{
			name: "reserved temporary path", path: "/" + temporaryWritePrefix + "x", options: OpenOptions{Write: true, Create: true},
			wantRoot: ErrInvalidPath, wantHost: ErrInvalidPath,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for index := range testFilesystems("") {
				dir, _ := newSymlinkFixture(t)
				if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600); err != nil {
					t.Fatal(err)
				}
				filesystem := testFilesystems(dir)[index]
				want := test.wantRoot
				if filesystem.name == "host" {
					want = test.wantHost
				}
				file, err := filesystem.fs.OpenFile(t.Context(), filesystem.path(test.path), test.options)
				assertError(t, filesystem.name, err, want)
				if want != nil {
					continue
				}
				verifyOpenedContent(t, file, test.write, test.wantContent, test.options)
			}
		})
	}
}

func verifyOpenedContent(t *testing.T, file *os.File, write, want string, options OpenOptions) {
	t.Helper()
	defer func() {
		_ = file.Close()
	}()
	if write != "" {
		if _, err := file.WriteString(write); err != nil {
			t.Fatal(err)
		}
	}
	if !options.Read {
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

func TestCreateAppliesSpecialModeBits(t *testing.T) {
	t.Parallel()

	for _, filesystem := range testFilesystems(t.TempDir()) {
		file, err := filesystem.fs.OpenFile(t.Context(), filesystem.path("/"+filesystem.name), OpenOptions{Write: true, Create: true, Mode: os.ModeSetuid | 0o750})
		if err != nil {
			t.Fatalf("%s: OpenFile: %v", filesystem.name, err)
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSetuid == 0 {
			t.Fatalf("%s: file mode = %v, want setuid", filesystem.name, info.Mode())
		}
	}
}
