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
	testCases := []struct {
		name       string
		dockerfile string
		want       bool
	}{
		{name: "syntax directive", dockerfile: "# syntax=docker/dockerfile:1.7\nFROM alpine:3.20\n", want: true},
		{name: "syntax directive without space", dockerfile: "#syntax=docker/dockerfile:1\nFROM alpine:3.20\n", want: true},
		{name: "syntax directive with spaces around equals", dockerfile: "# syntax = docker/dockerfile:1\nFROM alpine:3.20\n", want: true},
		{name: "run mount", dockerfile: "FROM oven/bun:alpine\nRUN --mount=type=cache,target=/root/.bun bun install\n", want: true},
		{name: "run mount after line continuation", dockerfile: "FROM alpine:3.20\nRUN \\\n  --mount=type=cache,target=/var/cache/apk apk add git\n", want: true},
		{name: "run network", dockerfile: "FROM alpine:3.20\nRUN --network=none echo hi\n", want: true},
		{name: "run security", dockerfile: "FROM alpine:3.20\nRUN --security=insecure echo hi\n", want: true},
		{name: "copy link", dockerfile: "FROM alpine:3.20\nCOPY --link app.txt /app.txt\n", want: true},
		{name: "add link with value", dockerfile: "FROM alpine:3.20\nADD --link=true app.txt /app.txt\n", want: true},
		{name: "mount flag inside shell command", dockerfile: "FROM alpine:3.20\nRUN echo '--mount=type=cache'\n", want: false},
		{name: "link inside copy source name", dockerfile: "FROM alpine:3.20\nCOPY --chown=1000 --link-target /dst\n", want: false},
		{name: "syntax comment after first instruction", dockerfile: "FROM alpine:3.20\n# syntax=docker/dockerfile:1\nRUN echo hi\n", want: false},
		{name: "plain dockerfile", dockerfile: "FROM alpine:3.20\nRUN echo hello\n", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dockerfileRequiresDirectBuildkitSessionInternal(tc.dockerfile)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("malformed dockerfile returns error", func(t *testing.T) {
		_, err := dockerfileRequiresDirectBuildkitSessionInternal("# only a comment\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse Dockerfile")
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
