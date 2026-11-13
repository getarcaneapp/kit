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
		dir     string
		cleanup func()
		err     error
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
		assert.ErrorIs(t, err, os.ErrNotExist)
		_, err = os.Stat(filepath.Join(got.dir, "app.txt"))
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("staging blocked on a FIFO in the build context")
	}
}
