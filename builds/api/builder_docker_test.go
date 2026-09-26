package api

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/buildkit/session"
	sessionauth "github.com/moby/buildkit/session/auth"
	dockerbuild "github.com/moby/moby/api/types/build"
	dockercontainer "github.com/moby/moby/api/types/container"
	dockerregistry "github.com/moby/moby/api/types/registry"
	dockerclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.getarcane.app/builds/types"
)

func TestPrepareDockerBuildInputInternal_RejectsMultiPlatform(t *testing.T) {
	contextDir := createBuildContextWithDockerfileInternal(t)
	req := types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
	}

	_, reportProgress, err := prepareDockerBuildInputInternal(req)
	require.Error(t, err)
	assert.True(t, reportProgress)
	assert.Contains(t, err.Error(), "does not support multi-platform builds")
}

func TestBuildDockerImageOptionsInternal_UsesBuildkitAndIncludesAuthConfigs(t *testing.T) {
	contextDir := createBuildContextWithDockerfileInternal(t)
	req := types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Tags:       []string{"ghcr.io/getarcaneapp/arcane:test"},
		Platforms:  []string{"linux/amd64"},
	}
	input, _, err := prepareDockerBuildInputInternal(req)
	require.NoError(t, err)

	authConfigs := map[string]dockerregistry.AuthConfig{
		"ghcr.io": {
			Username:      "db-user",
			Password:      "db-token",
			ServerAddress: "ghcr.io",
		},
	}

	buildOpts, err := buildDockerImageOptionsInternal(req, input, "Dockerfile", authConfigs)
	require.NoError(t, err)
	require.NotNil(t, buildOpts.AuthConfigs)
	assert.Equal(t, authConfigs, buildOpts.AuthConfigs)
	assert.Equal(t, dockerbuild.BuilderBuildKit, buildOpts.Version)
	require.Len(t, buildOpts.Platforms, 1)
	assert.Equal(t, "linux", buildOpts.Platforms[0].OS)
	assert.Equal(t, "amd64", buildOpts.Platforms[0].Architecture)
}

func TestBuildDockerImageOptionsInternal_EmptyAuthConfigsBecomesNil(t *testing.T) {
	contextDir := createBuildContextWithDockerfileInternal(t)
	req := types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Tags:       []string{"ghcr.io/getarcaneapp/arcane:test"},
	}
	input, _, err := prepareDockerBuildInputInternal(req)
	require.NoError(t, err)

	buildOpts, err := buildDockerImageOptionsInternal(req, input, "Dockerfile", map[string]dockerregistry.AuthConfig{})
	require.NoError(t, err)
	assert.Nil(t, buildOpts.AuthConfigs)
}

func TestPerformDockerBuildInternal_KeepsSessionActiveForRegistryAuth(t *testing.T) {
	testCases := []struct {
		name               string
		registryProvider   types.RegistryAuthProvider
		expectedUsername   string
		expectedCredential string
	}{
		{name: "anonymous"},
		{
			name: "saved registry credentials",
			registryProvider: fakeRegistryAuthProvider{authConfigs: map[string]dockerregistry.AuthConfig{
				"registry.example.com": {
					Username:      "db-user",
					Password:      "db-token",
					ServerAddress: "registry.example.com",
				},
			}},
			expectedUsername:   "db-user",
			expectedCredential: "db-token",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("DOCKER_CONFIG", t.TempDir())

			sessionManager, err := session.NewManager()
			require.NoError(t, err)
			managerCtx, cancelManager := context.WithCancel(context.Background())
			defer cancelManager()

			sessionIDCh := make(chan string, 1)
			credentialsCh := make(chan *sessionauth.CredentialsResponse, 1)
			serverErrCh := make(chan error, 2)
			server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/session"):
					if handleErr := sessionManager.HandleHTTPRequest(managerCtx, w, r); handleErr != nil {
						serverErrCh <- fmt.Errorf("handle session request: %w", handleErr)
					}
				case strings.HasSuffix(r.URL.Path, "/build"):
					if _, copyErr := io.Copy(io.Discard, r.Body); copyErr != nil {
						http.Error(w, copyErr.Error(), http.StatusInternalServerError)
						serverErrCh <- fmt.Errorf("consume build context: %w", copyErr)
						return
					}

					sessionID := r.URL.Query().Get("session")
					sessionIDCh <- sessionID
					lookupCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
					defer cancel()
					caller, lookupErr := sessionManager.Get(lookupCtx, sessionID, false)
					if lookupErr != nil {
						http.Error(w, lookupErr.Error(), http.StatusInternalServerError)
						serverErrCh <- fmt.Errorf("resolve build session: %w", lookupErr)
						return
					}

					credentials, credentialsErr := sessionauth.NewAuthClient(caller.Conn()).Credentials(lookupCtx, &sessionauth.CredentialsRequest{
						Host: "registry.example.com",
					})
					if credentialsErr != nil {
						http.Error(w, credentialsErr.Error(), http.StatusInternalServerError)
						serverErrCh <- fmt.Errorf("resolve registry credentials: %w", credentialsErr)
						return
					}
					credentialsCh <- credentials

					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, "{\"stream\":\"build complete\\n\"}\n")
				default:
					http.NotFound(w, r)
				}
			}))
			server.Start()

			dockerClient, err := dockerclient.New(
				dockerclient.WithHost("tcp://"+server.Listener.Addr().String()),
				dockerclient.WithAPIVersion("1.54"),
			)
			require.NoError(t, err)
			defer dockerClient.Close()

			service := &Service{registryAuthProvider: testCase.registryProvider}
			var progress bytes.Buffer
			err = service.performDockerBuildInternal(
				context.Background(),
				dockerClient,
				strings.NewReader("build context"),
				dockerclient.ImageBuildOptions{Version: dockerbuild.BuilderBuildKit},
				&progress,
			)
			require.NoError(t, err)
			assert.Contains(t, progress.String(), "build complete")

			sessionID := <-sessionIDCh
			assert.NotEmpty(t, sessionID)
			credentials := <-credentialsCh
			assert.Equal(t, testCase.expectedUsername, credentials.Username)
			assert.Equal(t, testCase.expectedCredential, credentials.Secret)
			select {
			case serverErr := <-serverErrCh:
				require.NoError(t, serverErr)
			default:
			}

			cancelManager()
			require.Eventually(t, func() bool {
				caller, lookupErr := sessionManager.Get(context.Background(), sessionID, true)
				return lookupErr == nil && caller == nil
			}, time.Second, 10*time.Millisecond)
		})
	}
}

func TestPerformDockerBuildInternal_ClosesSessionOnBuildError(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	sessionManager, err := session.NewManager()
	require.NoError(t, err)
	managerCtx, cancelManager := context.WithCancel(context.Background())
	defer cancelManager()

	sessionIDCh := make(chan string, 1)
	serverErrCh := make(chan error, 2)
	server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/session"):
			if handleErr := sessionManager.HandleHTTPRequest(managerCtx, w, r); handleErr != nil {
				serverErrCh <- fmt.Errorf("handle session request: %w", handleErr)
			}
		case strings.HasSuffix(r.URL.Path, "/build"):
			if _, copyErr := io.Copy(io.Discard, r.Body); copyErr != nil {
				http.Error(w, copyErr.Error(), http.StatusInternalServerError)
				serverErrCh <- fmt.Errorf("consume build context: %w", copyErr)
				return
			}

			sessionID := r.URL.Query().Get("session")
			sessionIDCh <- sessionID
			lookupCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			caller, lookupErr := sessionManager.Get(lookupCtx, sessionID, false)
			if lookupErr != nil || caller == nil {
				if lookupErr == nil {
					lookupErr = fmt.Errorf("session %q was not registered", sessionID)
				}
				serverErrCh <- lookupErr
			}
			http.Error(w, "build failed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	server.Start()

	dockerClient, err := dockerclient.New(
		dockerclient.WithHost("tcp://"+server.Listener.Addr().String()),
		dockerclient.WithAPIVersion("1.54"),
	)
	require.NoError(t, err)
	defer dockerClient.Close()

	service := &Service{}
	err = service.performDockerBuildInternal(
		context.Background(),
		dockerClient,
		strings.NewReader("build context"),
		dockerclient.ImageBuildOptions{Version: dockerbuild.BuilderBuildKit},
		io.Discard,
	)
	require.ErrorContains(t, err, "build failed")

	sessionID := <-sessionIDCh
	assert.NotEmpty(t, sessionID)
	select {
	case serverErr := <-serverErrCh:
		require.NoError(t, serverErr)
	default:
	}
	cancelManager()
	require.Eventually(t, func() bool {
		caller, lookupErr := sessionManager.Get(context.Background(), sessionID, true)
		return lookupErr == nil && caller == nil
	}, time.Second, 10*time.Millisecond)
}

func TestPrepareDockerBuildContextInternal_StagesInlineDockerfile(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "app.txt"), []byte("hello\n"), 0o644))

	req := types.BuildRequest{
		ContextDir:       contextDir,
		DockerfileInline: "FROM alpine:3.20\nCOPY app.txt /app.txt\n",
	}

	input, reportProgress, err := prepareDockerBuildInputInternal(req)
	require.NoError(t, err)
	assert.False(t, reportProgress)

	buildContextDir, dockerfileForBuild, cleanup, err := prepareDockerBuildContextInternal(input)
	require.NoError(t, err)
	defer cleanup()

	contents, err := os.ReadFile(filepath.Join(buildContextDir, filepath.FromSlash(dockerfileForBuild)))
	require.NoError(t, err)
	assert.Equal(t, "FROM alpine:3.20\nCOPY app.txt /app.txt\n", string(contents))

	appContents, err := os.ReadFile(filepath.Join(buildContextDir, "app.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(appContents))
}

func TestPrepareDockerBuildContextInternal_StagesDockerfileExcludedByDockerignore(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, ".dockerignore"), []byte("**/Dockerfile*\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "app.txt"), []byte("hello\n"), 0o644))

	req := types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
	}

	input, reportProgress, err := prepareDockerBuildInputInternal(req)
	require.NoError(t, err)
	assert.False(t, reportProgress)

	buildContextDir, dockerfileForBuild, cleanup, err := prepareDockerBuildContextInternal(input)
	require.NoError(t, err)
	defer cleanup()

	assert.NotEqual(t, contextDir, buildContextDir)
	assert.Equal(t, ".arcane.external.Dockerfile", dockerfileForBuild)

	contents, err := os.ReadFile(filepath.Join(buildContextDir, dockerfileForBuild))
	require.NoError(t, err)
	assert.Equal(t, "FROM alpine:3.20\n", string(contents))

	appContents, err := os.ReadFile(filepath.Join(buildContextDir, "app.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(appContents))
}

func TestDockerfileExcludedByDockerignoreInternal_ReturnsScannerError(t *testing.T) {
	contextDir := t.TempDir()
	tooLongPattern := strings.Repeat("a", bufio.MaxScanTokenSize+1)
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, ".dockerignore"), []byte(tooLongPattern), 0o644))

	excluded, err := dockerfileExcludedByDockerignoreInternal(contextDir, "Dockerfile")
	require.Error(t, err)
	assert.False(t, excluded)
	assert.Contains(t, err.Error(), "failed to read .dockerignore")
}

func TestPrepareDockerBuildContextInternal_StagesDockerfileExcludedByRootedDockerignorePattern(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, ".dockerignore"), []byte("/Dockerfile\n"), 0o644))

	input, _, err := prepareDockerBuildInputInternal(types.BuildRequest{ContextDir: contextDir, Dockerfile: "Dockerfile"})
	require.NoError(t, err)
	assert.True(t, input.dockerfileOutsideCtx)

	buildContextDir, dockerfileForBuild, cleanup, err := prepareDockerBuildContextInternal(input)
	require.NoError(t, err)
	defer cleanup()

	assert.Equal(t, ".arcane.external.Dockerfile", dockerfileForBuild)
	contents, err := os.ReadFile(filepath.Join(buildContextDir, dockerfileForBuild))
	require.NoError(t, err)
	assert.Equal(t, "FROM alpine:3.20\n", string(contents))
}

func TestReadDockerignoreInternal_ReturnsOpenError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced on Windows or when running as root")
	}

	contextDir := t.TempDir()
	ignorePath := filepath.Join(contextDir, ".dockerignore")
	require.NoError(t, os.WriteFile(ignorePath, []byte("Dockerfile\n"), 0o000))

	patterns, err := readDockerignoreInternal(contextDir)
	require.Error(t, err)
	assert.Nil(t, patterns)
	assert.Contains(t, err.Error(), "failed to read .dockerignore")
}

func TestReadDockerignoreInternal_MissingFileHasNoPatterns(t *testing.T) {
	patterns, err := readDockerignoreInternal(t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, patterns)
}

func TestPrepareBuildFilesystemInputInternal_DotDotPrefixedDockerfileIsInsideContext(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "..Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644))

	input, err := prepareBuildFilesystemInputInternal(types.BuildRequest{ContextDir: contextDir, Dockerfile: "..Dockerfile"})
	require.NoError(t, err)
	assert.False(t, input.dockerfileOutsideCtx)
	assert.Equal(t, "..Dockerfile", input.relDockerfile)
}

func TestParseUlimitsInternal(t *testing.T) {
	testCases := []struct {
		name    string
		values  map[string]string
		want    []*dockercontainer.Ulimit
		wantErr bool
	}{
		{name: "soft and hard", values: map[string]string{"nofile": "1024:2048"}, want: []*dockercontainer.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}}},
		{name: "single value sets soft and hard", values: map[string]string{"nofile": "4096"}, want: []*dockercontainer.Ulimit{{Name: "nofile", Soft: 4096, Hard: 4096}}},
		{name: "sorted by name", values: map[string]string{"nproc": "10", "nofile": "20"}, want: []*dockercontainer.Ulimit{{Name: "nofile", Soft: 20, Hard: 20}, {Name: "nproc", Soft: 10, Hard: 10}}},
		{name: "empty map", values: map[string]string{}, want: nil},
		{name: "soft above hard", values: map[string]string{"nofile": "2048:1024"}, wantErr: true},
		{name: "unknown name", values: map[string]string{"bogus": "1"}, wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseUlimitsInternal(tc.values)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid ulimit")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrepareDockerBuildInputInternal_RejectsInvalidUlimit(t *testing.T) {
	_, _, err := prepareDockerBuildInputInternal(types.BuildRequest{
		ContextDir: createBuildContextWithDockerfileInternal(t),
		Dockerfile: "Dockerfile",
		Ulimits:    map[string]string{"nofile": "2048:1024"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ulimit")
}

func TestBuildDockerImageOptionsInternal_NormalizesPlatformArchitecture(t *testing.T) {
	req := types.BuildRequest{
		ContextDir: createBuildContextWithDockerfileInternal(t),
		Dockerfile: "Dockerfile",
		Platforms:  []string{"linux/x86_64"},
	}
	input, _, err := prepareDockerBuildInputInternal(req)
	require.NoError(t, err)

	buildOpts, err := buildDockerImageOptionsInternal(req, input, "Dockerfile", nil)
	require.NoError(t, err)
	require.Len(t, buildOpts.Platforms, 1)
	assert.Equal(t, "linux", buildOpts.Platforms[0].OS)
	assert.Equal(t, "amd64", buildOpts.Platforms[0].Architecture)
}
