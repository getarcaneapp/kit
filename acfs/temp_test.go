package acfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMkdirTempCreatesUniqueDirectoriesFromPattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := t.Context()

	first, err := MkdirTemp(ctx, root, "/", "stage-*.tmp")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	second, err := MkdirTemp(ctx, root, "/", "stage-*.tmp")
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

	_, err = MkdirTemp(ctx, root, "/", "nested/pattern")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("MkdirTemp with multi-component pattern error = %v, want ErrInvalidPath", err)
	}
}
