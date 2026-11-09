//go:build linux

package acfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFromRejectsReplacedTemporaryFile(t *testing.T) {
	for index, named := range testFilesystems("") {
		t.Run(named.name, func(t *testing.T) {
			dir := t.TempDir()
			victim := filepath.Join(t.TempDir(), "victim")
			writeTestFile(t, victim, "victim")
			setPublishHooks(t, func(temporaryName string) {
				replaceWithLink(t, filepath.Join(dir, temporaryName), victim)
			}, nil)

			filesystem := testFilesystems(dir)[index]
			_, err := filesystem.fs.WriteFrom(t.Context(), filesystem.path("/target"), strings.NewReader("data"), WriteFromOptions{Size: 4, Mode: 0o644})
			if !errors.Is(err, ErrReplaced) {
				t.Fatalf("WriteFrom error = %v, want ErrReplaced", err)
			}
			requireMissing(t, filepath.Join(dir, "target"))
			if got := readTestFile(t, victim); got != "victim" {
				t.Fatalf("victim = %q", got)
			}
		})
	}
}

func TestWriteFromRejectsReplacedDestination(t *testing.T) {
	for index, named := range testFilesystems("") {
		t.Run(named.name, func(t *testing.T) {
			dir := t.TempDir()
			victim := filepath.Join(t.TempDir(), "victim")
			writeTestFile(t, victim, "victim")
			setPublishHooks(t, nil, func() {
				replaceWithLink(t, filepath.Join(dir, "target"), victim)
			})

			filesystem := testFilesystems(dir)[index]
			err := filesystem.fs.Write(t.Context(), filesystem.path("/target"), []byte("data"), WriteOptions{Mode: 0o644})
			if !errors.Is(err, ErrReplaced) {
				t.Fatalf("Write error = %v, want ErrReplaced", err)
			}
			if got := readTestFile(t, victim); got != "victim" {
				t.Fatalf("victim = %q", got)
			}
		})
	}
}

// relocatingReader moves the directory holding the write aside after the
// first read, then returns the rest of the content or fails.
type relocatingReader struct {
	t         *testing.T
	from, to  string
	remaining string
	fail      bool
	moved     bool
}

func (r *relocatingReader) Read(buffer []byte) (int, error) {
	if !r.moved {
		r.moved = true
		if err := os.Rename(r.from, r.to); err != nil {
			r.t.Error(err)
		}
	}
	if r.fail {
		return 0, errors.New("source failed")
	}
	if r.remaining == "" {
		return 0, io.EOF
	}
	n := copy(buffer, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}

func TestHostWriteFromStagesInOpenedParent(t *testing.T) {
	for _, fail := range []bool{false, true} {
		base := t.TempDir()
		from, to := filepath.Join(base, "parent"), filepath.Join(base, "moved")
		if err := os.Mkdir(from, 0o755); err != nil {
			t.Fatal(err)
		}
		reader := &relocatingReader{t: t, from: from, to: to, remaining: "data", fail: fail}
		_, err := Host{}.WriteFrom(t.Context(), filepath.Join(from, "target"), reader, WriteFromOptions{Size: 4, Mode: 0o644})
		if fail {
			if err == nil {
				t.Fatal("WriteFrom succeeded with a failing source")
			}
		} else if err != nil {
			t.Fatalf("WriteFrom: %v", err)
		}
		entries, err := os.ReadDir(to)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		want := "target"
		if fail {
			want = ""
		}
		if strings.Join(names, ",") != want {
			t.Fatalf("fail=%v: moved directory holds %q, want %q", fail, names, want)
		}
		requireMissing(t, from)
	}
}
