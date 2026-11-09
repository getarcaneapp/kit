package acfs

import (
	"errors"
	"os"
	"testing"
)

func TestOpenOptionsValidation(t *testing.T) {
	t.Parallel()

	for name, options := range map[string]OpenOptions{
		"no access":            {},
		"create only":          {Create: true, Mode: 0o600},
		"truncate read-only":   {Read: true, Truncate: true},
		"append read-only":     {Read: true, Append: true},
		"exclusive no create":  {Write: true, Exclusive: true},
		"directory mode flags": {Write: true, Create: true, Mode: os.ModeDir | 0o600},
	} {
		for _, filesystem := range testFilesystems(t.TempDir()) {
			_, err := filesystem.fs.OpenFile(t.Context(), filesystem.path("/file"), options)
			if !errors.Is(err, ErrInvalidOpenOptions) {
				t.Errorf("%s %s: error = %v, want ErrInvalidOpenOptions", filesystem.name, name, err)
			}
		}
	}
}

func TestOpenFileCreatesWithMode(t *testing.T) {
	t.Parallel()

	for _, filesystem := range testFilesystems(t.TempDir()) {
		target := filesystem.path("/" + filesystem.name)
		file, err := filesystem.fs.OpenFile(t.Context(), target, OpenOptions{Write: true, Create: true, Exclusive: true, Mode: 0o640})
		if err != nil {
			t.Fatalf("%s: %v", filesystem.name, err)
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&^0o640 != 0 {
			t.Fatalf("%s: mode = %v, want at most 0640", filesystem.name, info.Mode())
		}
	}
}

func TestHostOpenFileRejectsRelativePath(t *testing.T) {
	t.Parallel()

	_, err := Host{}.OpenFile(t.Context(), "relative", OpenOptions{Read: true})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}
