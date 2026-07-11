package api

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"

	"go.getarcane.app/builds/types"
)

func TestDockerfileRequiresDirectBuildkitSessionInternal(t *testing.T) {
	t.Run("syntax directive requires direct buildkit session", func(t *testing.T) {
		assert.True(t, dockerfileRequiresDirectBuildkitSessionInternal("# syntax=docker/dockerfile:1.7\nFROM alpine:3.20\n"))
	})

	t.Run("run mount requires direct buildkit session", func(t *testing.T) {
		assert.True(t, dockerfileRequiresDirectBuildkitSessionInternal("FROM oven/bun:alpine\nRUN --mount=type=cache,target=/root/.bun bun install\n"))
	})

	t.Run("plain dockerfile uses docker engine buildkit api", func(t *testing.T) {
		assert.False(t, dockerfileRequiresDirectBuildkitSessionInternal("FROM alpine:3.20\nRUN echo hello\n"))
	})
}

func TestRequiresDirectLocalBuildkitSessionInternal_ReadsRequestedDockerfile(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Dockerfile.custom"), []byte("FROM alpine:3.20\nRUN --mount=type=cache,target=/tmp/cache echo hi\n"), 0o644))

	required, err := requiresDirectLocalBuildkitSessionInternal(types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile.custom",
	})
	require.NoError(t, err)
	assert.True(t, required)
}

func TestRequiresDirectLocalBuildkitSessionInternal_UsesInlineDockerfile(t *testing.T) {
	required, err := requiresDirectLocalBuildkitSessionInternal(types.BuildRequest{
		ContextDir:       t.TempDir(),
		DockerfileInline: "FROM alpine:3.20\nRUN --mount=type=cache,target=/tmp/cache echo hi\n",
	})
	require.NoError(t, err)
	assert.True(t, required)
}
