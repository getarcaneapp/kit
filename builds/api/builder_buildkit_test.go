package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	buildkit "github.com/moby/buildkit/client"
	moby "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.getarcane.app/builds/types"
)

func TestBuildSolveOptInternal_StagesInlineDockerfile(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "app.txt"), []byte("hello\n"), 0o644))

	b := &Service{}
	req := types.BuildRequest{
		ContextDir:       contextDir,
		DockerfileInline: "FROM alpine:3.20\nCOPY app.txt /app.txt\n",
		BuildArgs: map[string]string{
			"FOO": "bar",
		},
	}

	solveOpt, loadErrCh, cleanup, err := b.buildSolveOptInternal(context.Background(), req, "local")
	require.NoError(t, err)
	defer cleanup()
	assert.Nil(t, loadErrCh)
	assert.Equal(t, ".arcane.inline.Dockerfile", solveOpt.FrontendAttrs["filename"])

	contextMount, ok := solveOpt.LocalMounts["context"]
	require.True(t, ok)
	dockerfileMount, ok := solveOpt.LocalMounts["dockerfile"]
	require.True(t, ok)

	dockerfile, err := dockerfileMount.Open(solveOpt.FrontendAttrs["filename"])
	require.NoError(t, err)
	contents, err := io.ReadAll(dockerfile)
	require.NoError(t, dockerfile.Close())
	require.NoError(t, err)
	assert.Equal(t, "FROM alpine:3.20\nCOPY app.txt /app.txt\n", string(contents))

	appFile, err := contextMount.Open("app.txt")
	require.NoError(t, err)
	appContents, err := io.ReadAll(appFile)
	require.NoError(t, appFile.Close())
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(appContents))
}

func TestBuildSolveOptInternal_LocalLoadUsesMobyExporter(t *testing.T) {
	contextDir := createBuildkitTestContext(t)
	b := &Service{}

	solveOpt, loadErrCh, cleanup, err := b.buildSolveOptInternal(context.Background(), types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Tags:       []string{"arcane.local/app:test"},
		Load:       true,
	}, "local")
	require.NoError(t, err)
	defer cleanup()

	require.Nil(t, loadErrCh)
	require.Len(t, solveOpt.Exports, 1)
	assert.Equal(t, "moby", solveOpt.Exports[0].Type)
	assert.Equal(t, "arcane.local/app:test", solveOpt.Exports[0].Attrs["name"])
	assert.NotContains(t, solveOpt.Exports[0].Attrs, "push")
	assert.Nil(t, solveOpt.Exports[0].Output)
}

func TestBuildSolveOptInternal_LocalPushAndLoadUsesSingleMobyExporter(t *testing.T) {
	contextDir := createBuildkitTestContext(t)
	b := &Service{}

	solveOpt, loadErrCh, cleanup, err := b.buildSolveOptInternal(context.Background(), types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Tags:       []string{"registry.example.com/app:test"},
		Push:       true,
		Load:       true,
	}, "local")
	require.NoError(t, err)
	defer cleanup()

	require.Nil(t, loadErrCh)
	require.Len(t, solveOpt.Exports, 1)
	assert.Equal(t, "moby", solveOpt.Exports[0].Type)
	assert.Equal(t, "registry.example.com/app:test", solveOpt.Exports[0].Attrs["name"])
	assert.NotContains(t, solveOpt.Exports[0].Attrs, "push")
}

func TestBuildSolveOptInternal_NonLocalLoadKeepsDockerExporter(t *testing.T) {
	contextDir := createBuildkitTestContext(t)
	server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.54/images/load" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		_, _ = w.Write([]byte("{}\n"))
	}))
	httpClient := server.Client()

	client, err := moby.New(
		moby.WithHost(server.URL),
		moby.WithHTTPClient(httpClient),
		moby.WithAPIVersion("1.54"),
	)
	require.NoError(t, err)

	b := &Service{dockerClientProvider: testDockerClientProvider{client: client}}
	solveOpt, loadErrCh, cleanup, err := b.buildSolveOptInternal(context.Background(), types.BuildRequest{
		ContextDir: contextDir,
		Dockerfile: "Dockerfile",
		Tags:       []string{"arcane.local/app:test"},
		Load:       true,
	}, "depot")
	require.NoError(t, err)
	defer cleanup()

	require.NotNil(t, loadErrCh)
	require.Len(t, solveOpt.Exports, 1)
	assert.Equal(t, "docker", solveOpt.Exports[0].Type)
	require.NotNil(t, solveOpt.Exports[0].Output)

	output, err := solveOpt.Exports[0].Output(nil)
	require.NoError(t, err)
	require.NoError(t, output.Close())
	require.NoError(t, <-loadErrCh)
}

func createBuildkitTestContext(t *testing.T) string {
	t.Helper()
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644))
	return contextDir
}

type testDockerClientProvider struct {
	client *moby.Client
}

func (p testDockerClientProvider) GetClient(context.Context) (*moby.Client, error) {
	return p.client, nil
}

func TestParseBuildkitCacheEntriesInternal(t *testing.T) {
	testCases := []struct {
		name    string
		values  []string
		want    []buildkit.CacheOptionsEntry
		wantErr bool
	}{
		{
			name:   "plain ref shorthand",
			values: []string{"ghcr.io/getarcaneapp/cache:main"},
			want:   []buildkit.CacheOptionsEntry{{Type: "registry", Attrs: map[string]string{"ref": "ghcr.io/getarcaneapp/cache:main"}}},
		},
		{
			name:   "quoted field keeps its comma",
			values: []string{`type=registry,ref=foo,"mode=max,compression=zstd"`},
			want:   []buildkit.CacheOptionsEntry{{Type: "registry", Attrs: map[string]string{"ref": "foo", "mode": "max,compression=zstd"}}},
		},
		{
			name:   "type defaults to registry",
			values: []string{"ref=foo,mode=max"},
			want:   []buildkit.CacheOptionsEntry{{Type: "registry", Attrs: map[string]string{"ref": "foo", "mode": "max"}}},
		},
		{
			name:   "blank values are skipped",
			values: []string{"", "  "},
			want:   nil,
		},
		{name: "bad quote", values: []string{`type=registry,ref="foo`}, wantErr: true},
		{name: "field without equals", values: []string{"type=local,dest"}, wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBuildkitCacheEntriesInternal(tc.values)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid cache entry")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
