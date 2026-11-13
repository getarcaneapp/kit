package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configtypes "github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/session/auth/authprovider"
	dockerregistry "github.com/moby/moby/api/types/registry"
	moby "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kit.getarcane.app/builds/types"
)

type (
	fakeRegistryAuthProvider struct {
		authConfigs map[string]dockerregistry.AuthConfig
		err         error
	}

	testSettingsProvider struct{}
)

func (f fakeRegistryAuthProvider) GetAllRegistryAuthConfigs(_ context.Context) (map[string]dockerregistry.AuthConfig, error) {
	return f.authConfigs, f.err
}

func TestBuildkitAuthConfigProvider_UsesRegistryAuthProviderForHost(t *testing.T) {
	registryCfg := dockerregistry.AuthConfig{
		Username:      "db-user",
		Password:      "db-token",
		ServerAddress: "ghcr.io",
	}

	provider := buildkitAuthConfigProvider(
		func(_ context.Context, _ string, _ []string, _ authprovider.ExpireCachedAuthCheck) (configtypes.AuthConfig, error) {
			return configtypes.AuthConfig{Username: "default"}, nil
		},
		fakeRegistryAuthProvider{authConfigs: map[string]dockerregistry.AuthConfig{"ghcr.io": registryCfg}},
	)

	cfg, err := provider(t.Context(), "ghcr.io", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "db-user", cfg.Username)
	assert.Equal(t, "db-token", cfg.Password)
	assert.Equal(t, "ghcr.io", cfg.ServerAddress)
}

func TestBuildkitAuthConfigProvider_FallsBackToDefaultProvider(t *testing.T) {
	defaultCalled := false
	defaultCfg := configtypes.AuthConfig{Username: "default-user", Password: "default-token", ServerAddress: "docker.io"}

	t.Run("registry provider error", func(t *testing.T) {
		defaultCalled = false
		provider := buildkitAuthConfigProvider(
			func(_ context.Context, _ string, _ []string, _ authprovider.ExpireCachedAuthCheck) (configtypes.AuthConfig, error) {
				defaultCalled = true
				return defaultCfg, nil
			},
			fakeRegistryAuthProvider{err: errors.New("db unavailable")},
		)

		cfg, err := provider(t.Context(), "docker.io", nil, nil)
		require.NoError(t, err)
		assert.True(t, defaultCalled)
		assert.Equal(t, defaultCfg, cfg)
	})

	t.Run("registry provider missing host", func(t *testing.T) {
		defaultCalled = false
		provider := buildkitAuthConfigProvider(
			func(_ context.Context, _ string, _ []string, _ authprovider.ExpireCachedAuthCheck) (configtypes.AuthConfig, error) {
				defaultCalled = true
				return defaultCfg, nil
			},
			fakeRegistryAuthProvider{authConfigs: map[string]dockerregistry.AuthConfig{"ghcr.io": {Username: "db-user"}}},
		)

		cfg, err := provider(t.Context(), "docker.io", nil, nil)
		require.NoError(t, err)
		assert.True(t, defaultCalled)
		assert.Equal(t, defaultCfg, cfg)
	})
}

func TestWrapBuildkitSolveError_LeavesGenericErrorsUnchanged(t *testing.T) {
	err := errors.New("failed to solve: Dockerfile parse error on line 3")

	wrapped := wrapBuildkitSolveError(err, "local")

	require.ErrorIs(t, wrapped, err)
	assert.Equal(t, err.Error(), wrapped.Error())
}

func TestWrapBuildkitSolveError_WrapsDockerAndImageExporterErrors(t *testing.T) {
	t.Run("docker exporter", func(t *testing.T) {
		rawErr := errors.New(`failed to solve: exporter "docker" could not be found`)
		wrapped := wrapBuildkitSolveError(rawErr, "local")

		require.ErrorIs(t, wrapped, rawErr)
		assert.Contains(t, wrapped.Error(), "the Docker Engine embedded BuildKit requires the docker image-store exporter")
	})

	t.Run("image exporter", func(t *testing.T) {
		rawErr := errors.New(`failed to solve: exporter "image" could not be found`)
		wrapped := wrapBuildkitSolveError(rawErr, "depot")

		require.ErrorIs(t, wrapped, rawErr)
		assert.Contains(t, wrapped.Error(), "depot and remote BuildKit providers require the image exporter")
	})
}

func (testSettingsProvider) BuildSettings() types.BuildSettings {
	return types.BuildSettings{}
}

func TestNewServiceReturnsBuildEngine(t *testing.T) {
	service, err := NewService(Config{
		SettingsProvider:     testSettingsProvider{},
		DockerClientProvider: testDockerClientProvider{},
	})
	require.NoError(t, err)

	var _ types.Builder = service
}

func TestNewServiceRequiresProviders(t *testing.T) {
	_, err := NewService(Config{DockerClientProvider: testDockerClientProvider{}})
	require.ErrorAs(t, err, new(*types.BuildSettingsProviderUnavailableError))

	_, err = NewService(Config{SettingsProvider: testSettingsProvider{}})
	require.ErrorAs(t, err, new(*types.BuildDockerServiceUnavailableError))
}

func TestBuildImage_DropsBlankPlatforms(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644))

	platformCh := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/build") {
			_, _ = io.Copy(io.Discard, r.Body)
			platformCh <- r.URL.Query()["platform"]
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	dockerClient, err := moby.New(
		moby.WithHost("tcp://"+server.Listener.Addr().String()),
		moby.WithAPIVersion("1.54"),
	)
	require.NoError(t, err)
	defer func() { _ = dockerClient.Close() }()

	service, err := NewService(Config{
		SettingsProvider:     testSettingsProvider{},
		DockerClientProvider: testDockerClientProvider{client: dockerClient},
	})
	require.NoError(t, err)

	_, err = service.BuildImage(t.Context(), types.BuildRequest{
		ContextDir: contextDir,
		Tags:       []string{"arcane.local/app:test"},
		Platforms:  []string{" ", "linux/amd64", ""},
	}, io.Discard, "")
	require.Error(t, err)
	var multiPlatformErr *types.DockerBuildMultiPlatformUnsupportedError
	require.NotErrorAs(t, err, &multiPlatformErr)
	assert.Len(t, <-platformCh, 1)
}
