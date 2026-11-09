package acfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSync(t *testing.T) {
	t.Parallel()

	for index, named := range testFilesystems("") {
		t.Run(named.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			filesystem := testFilesystems(dir)[index]
			if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, filepath.Join(dir, "sub", "file"), "data", 0o600)

			for _, logicalPath := range []string{"/", "/sub", "/sub/file"} {
				if err := filesystem.fs.Sync(t.Context(), filesystem.path(logicalPath)); err != nil {
					t.Errorf("Sync(%q): %v", logicalPath, err)
				}
			}
			if err := filesystem.fs.Sync(t.Context(), filesystem.path("/missing")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("Sync(missing) = %v, want fs.ErrNotExist", err)
			}
		})
	}
}
