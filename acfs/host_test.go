package acfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acfstypes "kit.getarcane.app/acfs/types"
)

func TestHostRejectsInvalidPaths(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	host := Host{}
	operations := map[string]func(hostPath string) error{
		"List": func(p string) error {
			_, err := host.List(ctx, p)
			return err
		},
		"Stat": func(p string) error {
			_, err := host.Stat(ctx, p, acfstypes.StatOptions{})
			return err
		},
		"Read": func(p string) error {
			_, err := host.ReadFile(ctx, p)
			return err
		},
		"Write": func(p string) error {
			return host.Write(ctx, p, nil, WriteOptions{Mode: 0o600})
		},
		"Mkdir": func(p string) error {
			return host.MkdirAll(ctx, p, 0o755)
		},
		"Remove": func(p string) error {
			return errors.Join(host.Remove(ctx, p), host.RemoveAll(ctx, p))
		},
		"Rename": func(p string) error {
			return host.Rename(ctx, p, "/tmp/target")
		},
	}
	for _, hostPath := range []string{"", "relative", "./relative", "../up", "/with\x00nul", "/tmp/" + temporaryWritePrefix + "x"} {
		for name, operation := range operations {
			if err := operation(hostPath); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("%s(%q) error = %v, want ErrInvalidPath", name, hostPath, err)
			}
		}
	}
}

func TestHostRefusesToRemoveFilesystemRoot(t *testing.T) {
	t.Parallel()

	for _, hostPath := range []string{"/", "//", "/tmp/..", `C:\`, `C:\Windows\..`, `\\server\share\`} {
		if !filepath.IsAbs(hostPath) {
			continue
		}
		if err := (Host{}).Remove(t.Context(), hostPath); !errors.Is(err, ErrRootRemoval) {
			t.Errorf("Remove(%q) error = %v, want ErrRootRemoval", hostPath, err)
		}
		if err := (Host{}).RemoveAll(t.Context(), hostPath); !errors.Is(err, ErrRootRemoval) {
			t.Errorf("RemoveAll(%q) error = %v, want ErrRootRemoval", hostPath, err)
		}
	}
}

func TestHostRenameRefusesFilesystemRoot(t *testing.T) {
	t.Parallel()

	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	err := Host{}.Rename(t.Context(), root, filepath.Join(t.TempDir(), "moved"))
	if !errors.Is(err, ErrRootRemoval) {
		t.Fatalf("Rename(%q) error = %v, want ErrRootRemoval", root, err)
	}
	err = Host{}.Rename(t.Context(), filepath.Join(t.TempDir(), "missing"), root)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Rename onto %q error = %v, want ErrInvalidPath", root, err)
	}
}

func TestHostReadsPseudoFilesToEOF(t *testing.T) {
	t.Parallel()

	const pseudoFile = "/proc/self/cmdline"
	if info, err := os.Stat(pseudoFile); err != nil || info.Size() != 0 {
		t.Skipf("%s is not a zero-size pseudo-file here: %v", pseudoFile, err)
	}
	content, err := Host{}.ReadFile(t.Context(), pseudoFile)
	if err != nil || len(content) == 0 {
		t.Fatalf("ReadFile = %d bytes, %v; want the command line", len(content), err)
	}
	var destination bytes.Buffer
	written, err := Host{}.ReadTo(t.Context(), pseudoFile, &destination, 0)
	if err != nil || written == 0 || int64(destination.Len()) != written {
		t.Fatalf("ReadTo = %d (%d buffered), %v; want the command line", written, destination.Len(), err)
	}
	written, err = Host{}.ReadTo(t.Context(), pseudoFile, io.Discard, 1)
	if err != nil || written != 1 {
		t.Fatalf("ReadTo limited = %d, %v; want 1", written, err)
	}
}

func TestHostWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want     error
		name     string
		path     string
		readBack string
		options  WriteOptions
	}{
		{name: "atomic replace", path: "file", options: WriteOptions{Mode: 0o600}, readBack: "file"},
		{name: "atomic replaces final symlink", path: "file-link", options: WriteOptions{Mode: 0o600}, readBack: "file-link"},
		{name: "in place", path: "file", options: WriteOptions{Mode: 0o600, InPlace: true}, readBack: "file"},
		{name: "in place refuses final symlink", path: "file-link", options: WriteOptions{Mode: 0o600, InPlace: true}, want: ErrSymlink},
		{name: "directory", path: "dir", options: WriteOptions{Mode: 0o600}, want: errAnyFailure},
		{name: "root directory", path: "/", options: WriteOptions{Mode: 0o600}, want: ErrIsDirectory},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir, _ := newSymlinkFixture(t)
			target := filepath.Join(dir, test.path)
			if test.path == "/" {
				target = "/"
			}
			err := Host{}.Write(t.Context(), target, []byte("written"), test.options)
			assertError(t, "Write", err, test.want)
			if test.want != nil {
				return
			}
			content, err := os.ReadFile(filepath.Join(dir, test.readBack))
			if err != nil || string(content) != "written" {
				t.Fatalf("content = %q, %v; want written", content, err)
			}
			if test.path == "file-link" {
				info, lstatErr := os.Lstat(target)
				if lstatErr != nil || info.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("atomic write kept the symlink: %v, %v", info, lstatErr)
				}
			}
		})
	}
}

func TestHostHidesReservedWriteNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, temporaryWritePrefix+"fixture"), "internal", 0o600)
	writeFixtureFile(t, filepath.Join(dir, "visible"), "x", 0o600)
	entries, err := Host{}.List(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "visible" {
		t.Fatalf("List = %#v, want only visible", entries)
	}
}

func TestHostWriteFromAndReadTo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "payload")
	written, err := Host{}.WriteFrom(t.Context(), target, strings.NewReader("abc"), WriteFromOptions{Size: 3, Mode: 0o600})
	if err != nil || written != 3 {
		t.Fatalf("WriteFrom = %d, %v", written, err)
	}
	_, err = Host{}.WriteFrom(t.Context(), target, strings.NewReader("ab"), WriteFromOptions{Size: 3, Mode: 0o600})
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("short WriteFrom error = %v, want ErrSizeMismatch", err)
	}
	if err = (Host{}).WriteAt(t.Context(), target, 1, []byte("Z")); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	if _, err = (Host{}).ReadTo(t.Context(), target, &builder, 0); err != nil || builder.String() != "aZc" {
		t.Fatalf("ReadTo = %q, %v; want aZc", builder.String(), err)
	}
	reader, size, err := Host{}.OpenReadSeek(t.Context(), target)
	if err != nil || size != 3 {
		t.Fatalf("OpenReadSeek = %d, %v", size, err)
	}
	defer func() {
		_ = reader.Close()
	}()
	if _, err = reader.Seek(2, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil || string(rest) != "c" {
		t.Fatalf("read after seek = %q, %v", rest, err)
	}
}

func TestHostCopyAndMirrorDir(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	writeFixtureFile(t, filepath.Join(source, "a"), "a", 0o600)
	if err := os.Mkdir(filepath.Join(source, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(source, "sub", "b"), "b", 0o600)
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Host{}.CopyDir(t.Context(), source, destination, acfstypes.CopyOptions{})
	if err != nil || result.Copied != 2 {
		t.Fatalf("CopyDir = %+v, %v; want 2 copied", result, err)
	}
	writeFixtureFile(t, filepath.Join(destination, "stale"), "x", 0o600)
	if err = (Host{}).MirrorDir(t.Context(), source, destination, acfstypes.MirrorOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(destination, "stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MirrorDir kept stale entry: %v", err)
	}
	if _, err = (Host{}).CopyDir(t.Context(), source, filepath.Join(source, "sub", "nested"), acfstypes.CopyOptions{}); err == nil {
		t.Fatal("CopyDir into its own source succeeded")
	}
	if _, err = (Host{}).CopyDir(t.Context(), "relative", destination, acfstypes.CopyOptions{}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("CopyDir relative error = %v, want ErrInvalidPath", err)
	}
}

func BenchmarkHostList(b *testing.B) {
	dir := b.TempDir()
	writeBenchmarkEntries(b, dir, 1_000)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (Host{}).List(b.Context(), dir); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHostStat(b *testing.B) {
	root := newStatBenchmarkRoot(b)
	target := filepath.Join(root.Path, "project", "config", "compose.yaml")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (Host{}).Stat(b.Context(), target, acfstypes.StatOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHostReadFile(b *testing.B) {
	const size = 4 << 10
	target := filepath.Join(b.TempDir(), "payload")
	writeBenchmarkPayload(b, target, size)
	b.ReportAllocs()
	b.SetBytes(size)
	for b.Loop() {
		if _, err := (Host{}).ReadFile(b.Context(), target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHostWrite(b *testing.B) {
	payloads := [2][]byte{bytes.Repeat([]byte("a"), 4<<10), bytes.Repeat([]byte("b"), 4<<10)}
	for _, inPlace := range []bool{false, true} {
		b.Run(fmt.Sprintf("bytes-4096-in-place-%t", inPlace), func(b *testing.B) {
			target := filepath.Join(b.TempDir(), "payload")
			b.ReportAllocs()
			b.SetBytes(4 << 10)
			iteration := 0
			for b.Loop() {
				err := Host{}.Write(b.Context(), target, payloads[iteration%2], WriteOptions{Mode: 0o600, InPlace: inPlace})
				if err != nil {
					b.Fatal(err)
				}
				iteration++
			}
		})
	}
}
