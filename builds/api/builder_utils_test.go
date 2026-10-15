package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.getarcane.app/builds/types"
)

func createBuildContextWithDockerfile(t *testing.T) string {
	t.Helper()

	contextDir := t.TempDir()
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	require.NoError(t, os.WriteFile(dockerfilePath, []byte("FROM alpine:3.20\n"), 0o644))
	return contextDir
}

func TestValidateBuildRequest_LocalProviderUnsupportedOptions(t *testing.T) {
	contextDir := createBuildContextWithDockerfile(t)
	req := types.BuildRequest{
		ContextDir:   contextDir,
		Dockerfile:   "Dockerfile",
		Tags:         []string{"ghcr.io/getarcaneapp/arcane:test"},
		Load:         true,
		CacheTo:      []string{"type=registry,ref=ghcr.io/getarcaneapp/cache:latest"},
		Entitlements: []string{"network.host"},
		Privileged:   true,
		Platforms:    []string{"linux/amd64", "linux/arm64"},
	}

	err := validateBuildRequest(req, "local")
	require.EqualError(t, err, "unsupported build options for provider local: cacheTo, entitlements, platforms, privileged")
}

func TestValidateBuildRequest_DepotProviderUnsupportedOptions(t *testing.T) {
	contextDir := createBuildContextWithDockerfile(t)
	req := types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Tags:       []string{"ghcr.io/getarcaneapp/arcane:test"},
		Push:       true,
		Network:    "host",
		Isolation:  "process",
		ShmSize:    128 * 1024 * 1024,
		Ulimits: map[string]string{
			"nofile": "1024:2048",
		},
		ExtraHosts: []string{"registry.local:10.0.0.5"},
	}

	err := validateBuildRequest(req, "depot")
	require.EqualError(t, err, "unsupported build options for provider depot: extraHosts, isolation, network, shmSize, ulimits")
}

func TestValidateBuildRequest_RespectsTrimmedValues(t *testing.T) {
	contextDir := createBuildContextWithDockerfile(t)
	req := types.BuildRequest{
		ContextDir:   contextDir,
		Dockerfile:   "Dockerfile",
		Tags:         []string{"ghcr.io/getarcaneapp/arcane:test"},
		Load:         true,
		CacheTo:      []string{"   "},
		Entitlements: []string{"\n\t"},
		ExtraHosts:   []string{"  "},
		Platforms:    []string{"linux/amd64", "   "},
	}

	err := validateBuildRequest(req, "local")
	assert.NoError(t, err)
}

func TestValidateBuildRequest_AllowsInlineDockerfile(t *testing.T) {
	contextDir := t.TempDir()
	req := types.BuildRequest{
		ContextDir:       contextDir,
		DockerfileInline: "FROM alpine:3.20\nRUN echo inline\n",
		Tags:             []string{"ghcr.io/getarcaneapp/arcane:test"},
		Load:             true,
	}

	err := validateBuildRequest(req, "local")
	assert.NoError(t, err)
}

func TestValidateBuildRequest_RejectsDockerfileAndInlineTogether(t *testing.T) {
	contextDir := createBuildContextWithDockerfile(t)
	req := types.BuildRequest{
		ContextDir:       contextDir,
		Dockerfile:       "Dockerfile",
		DockerfileInline: "FROM alpine:3.20\n",
		Tags:             []string{"ghcr.io/getarcaneapp/arcane:test"},
		Load:             true,
	}

	err := validateBuildRequest(req, "local")
	require.EqualError(t, err, "dockerfile and dockerfileInline are mutually exclusive")
}

func TestValidateBuildRequest_ReturnsCommonTypedErrors(t *testing.T) {
	err := validateBuildRequest(types.BuildRequest{}, "local")
	require.Error(t, err)

	var typedErr *types.BuildContextDirRequiredError
	assert.ErrorAs(t, err, &typedErr)
}

func TestPrepareDockerBuildInput_ReturnsCommonTypedErrors(t *testing.T) {
	contextDir := createBuildContextWithDockerfile(t)
	req := types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
	}

	_, reportProgress, err := prepareDockerBuildInput(req)
	require.Error(t, err)
	assert.True(t, reportProgress)

	var typedErr *types.DockerBuildMultiPlatformUnsupportedError
	assert.ErrorAs(t, err, &typedErr)
}
