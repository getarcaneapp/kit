//go:build unix

package api

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareBuildContext_SkipsFIFO(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(contextDir, "pipe"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "app.txt"), []byte("hello\n"), 0o644))

	type staged struct {
		err     error
		cleanup func()
		dir     string
	}
	done := make(chan staged, 1)
	go func() {
		dir, _, cleanup, err := prepareBuildContext(buildFilesystemInput{
			contextDir:           contextDir,
			relDockerfile:        ".arcane.inline.Dockerfile",
			dockerfileInline:     "FROM alpine:3.20\n",
			dockerfileOutsideCtx: true,
		})
		done <- staged{dir: dir, cleanup: cleanup, err: err}
	}()

	select {
	case got := <-done:
		require.NoError(t, got.err)
		defer got.cleanup()
		_, err := os.Lstat(filepath.Join(got.dir, "pipe"))
		require.ErrorIs(t, err, os.ErrNotExist)
		_, err = os.Stat(filepath.Join(got.dir, "app.txt"))
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("staging blocked on a FIFO in the build context")
	}
}

func TestPrepareBuildContext_StagesReadOnlyDirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	contextDir := t.TempDir()
	readOnly := filepath.Join(contextDir, "ro")
	require.NoError(t, os.MkdirAll(filepath.Join(readOnly, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(readOnly, "nested", "app.txt"), []byte("hello\n"), 0o644))
	require.NoError(t, os.Chmod(filepath.Join(readOnly, "nested"), 0o555))
	require.NoError(t, os.Chmod(readOnly, 0o555))
	t.Cleanup(func() {
		_ = os.Chmod(readOnly, 0o755)
		_ = os.Chmod(filepath.Join(readOnly, "nested"), 0o755)
	})

	dir, _, cleanup, err := prepareBuildContext(buildFilesystemInput{
		contextDir:           contextDir,
		relDockerfile:        ".arcane.inline.Dockerfile",
		dockerfileInline:     "FROM alpine:3.20\n",
		dockerfileOutsideCtx: true,
	})
	require.NoError(t, err)

	contents, err := os.ReadFile(filepath.Join(dir, "ro", "nested", "app.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(contents))
	for _, sub := range []string{"ro", filepath.Join("ro", "nested")} {
		info, statErr := os.Stat(filepath.Join(dir, sub))
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o555), info.Mode().Perm(), sub)
	}

	cleanup()
	_, err = os.Lstat(dir)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
