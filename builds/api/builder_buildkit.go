package api

import (
	"cmp"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/cli/cli/config"
	buildkit "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/tonistiigi/fsutil"
	kit "go.getarcane.app/kit/pkg"

	dockerutils "go.getarcane.app/builds/pkg/docker"
	"go.getarcane.app/builds/types"
)

// parseBuildkitCacheEntries parses --cache-from/--cache-to style values.
// A value without "=" is shorthand for a registry cache ref; otherwise it is a
// CSV list of key=value attributes, so quoted fields may contain commas.
func parseBuildkitCacheEntries(values []string) ([]buildkit.CacheOptionsEntry, error) {
	entries := make([]buildkit.CacheOptionsEntry, 0, len(values))

	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		if !strings.Contains(raw, "=") {
			entries = append(entries, buildkit.CacheOptionsEntry{
				Type:  "registry",
				Attrs: map[string]string{"ref": raw},
			})
			continue
		}

		fields, err := csv.NewReader(strings.NewReader(raw)).Read()
		if err != nil {
			return nil, fmt.Errorf("invalid cache entry %q: %w", raw, err)
		}

		cacheType := ""
		attrs := map[string]string{}
		for _, field := range fields {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}

			key, value, ok := strings.Cut(field, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				return nil, fmt.Errorf("invalid cache entry %q: field %q is not key=value", raw, field)
			}
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}

			if key == "type" {
				cacheType = value
				continue
			}

			attrs[key] = value
		}

		entries = append(entries, buildkit.CacheOptionsEntry{
			Type:  cmp.Or(cacheType, "registry"),
			Attrs: attrs,
		})
	}

	if len(entries) == 0 {
		return nil, nil
	}

	return entries, nil
}

func (b *Service) buildSolveOpt(ctx context.Context, req types.BuildRequest, providerName string) (buildkit.SolveOpt, <-chan error, func(), error) {
	fsInput, err := prepareBuildFilesystemInput(req)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}

	cacheImports, err := parseBuildkitCacheEntries(req.CacheFrom)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}
	cacheExports, err := parseBuildkitCacheEntries(req.CacheTo)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}

	contextDir, dockerfilePath, cleanup, err := prepareBuildContext(fsInput)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}

	dockerfileRelDir := filepath.Dir(filepath.FromSlash(dockerfilePath))

	frontendAttrs := map[string]string{
		"filename": kit.Ternary(dockerfileRelDir == ".", dockerfilePath, filepath.Base(dockerfilePath)),
	}
	if strings.TrimSpace(req.Target) != "" {
		frontendAttrs["target"] = strings.TrimSpace(req.Target)
	}
	if req.NoCache {
		frontendAttrs["no-cache"] = ""
	}
	if req.Pull {
		frontendAttrs["image-resolve-mode"] = "pull"
	}
	if len(req.Platforms) > 0 {
		frontendAttrs["platform"] = strings.Join(req.Platforms, ",")
	}
	for key, val := range req.BuildArgs {
		frontendAttrs["build-arg:"+key] = val
	}
	for key, val := range req.Labels {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		frontendAttrs["label:"+k] = val
	}

	contextMount, err := fsutil.NewFS(contextDir)
	if err != nil {
		cleanup()
		return buildkit.SolveOpt{}, nil, nil, fmt.Errorf("failed to prepare build context mount: %w", err)
	}
	dockerfileMount, err := fsutil.NewFS(kit.Ternary(dockerfileRelDir == ".", contextDir, filepath.Join(contextDir, dockerfileRelDir)))
	if err != nil {
		cleanup()
		return buildkit.SolveOpt{}, nil, nil, fmt.Errorf("failed to prepare Dockerfile mount: %w", err)
	}

	exports, loadErrCh, err := func() ([]buildkit.ExportEntry, <-chan error, error) {
		if providerName == "local" {
			if !req.Load && !req.Push {
				return nil, nil, nil
			}
			return []buildkit.ExportEntry{{
				Type:  "moby",
				Attrs: map[string]string{"name": strings.Join(req.Tags, ",")},
			}}, nil, nil
		}

		var exports []buildkit.ExportEntry
		if req.Push {
			exports = append(exports, buildkit.ExportEntry{
				Type: "image",
				Attrs: map[string]string{
					"name":           strings.Join(req.Tags, ","),
					"push":           "true",
					"oci-mediatypes": "true",
				},
			})
		}
		if !req.Load {
			return exports, nil, nil
		}

		if b.dockerClientProvider == nil {
			return nil, nil, &types.BuildDockerServiceUnavailableError{}
		}

		dockerClient, clientErr := b.dockerClientProvider.GetClient(ctx)
		if clientErr != nil {
			return nil, nil, fmt.Errorf("failed to connect to Docker: %w", clientErr)
		}

		pr, pw := io.Pipe()
		loadErrCh := make(chan error, 1)
		go func() {
			defer func() { _ = pr.Close() }()
			loadResp, loadErr := dockerClient.ImageLoad(ctx, pr)
			if loadErr != nil {
				loadErrCh <- loadErr
				return
			}
			defer func() { _ = loadResp.Close() }()
			loadErrCh <- dockerutils.RenderJSONMessageStream(loadResp, io.Discard)
		}()

		return append(exports, buildkit.ExportEntry{
			Type:  "docker",
			Attrs: kit.Ternary(len(req.Tags) > 0, map[string]string{"name": strings.Join(req.Tags, ",")}, map[string]string{}),
			Output: func(_ map[string]string) (io.WriteCloser, error) {
				return pw, nil
			},
		}), loadErrCh, nil
	}()
	if err != nil {
		cleanup()
		return buildkit.SolveOpt{}, nil, nil, err
	}

	return buildkit.SolveOpt{
		Exports: exports,
		LocalMounts: map[string]fsutil.FS{
			"context":    contextMount,
			"dockerfile": dockerfileMount,
		},
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
		CacheExports:  cacheExports,
		CacheImports:  cacheImports,
		Session: []session.Attachable{
			authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
				AuthConfigProvider: buildkitAuthConfigProvider(authprovider.LoadAuthConfig(config.LoadDefaultConfigFile(os.Stderr)), b.registryAuthProvider),
			}),
		},
		AllowedEntitlements: kit.Unique(append(kit.TrimNonEmpty(req.Entitlements), kit.Ternary(req.Privileged, []string{"security.insecure"}, nil)...)),
	}, loadErrCh, cleanup, nil
}
