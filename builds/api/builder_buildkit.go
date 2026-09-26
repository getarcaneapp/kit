package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	buildkit "github.com/moby/buildkit/client"
	"github.com/tonistiigi/fsutil"
	dockerutils "go.getarcane.app/builds/pkg/docker"
	"go.getarcane.app/builds/types"
)

// parseBuildkitCacheEntriesInternal parses --cache-from/--cache-to style values.
// A value without "=" is shorthand for a registry cache ref; otherwise it is a
// CSV list of key=value attributes, so quoted fields may contain commas.
func parseBuildkitCacheEntriesInternal(values []string) ([]buildkit.CacheOptionsEntry, error) {
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

		entry := buildkit.CacheOptionsEntry{Attrs: map[string]string{}}
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
				entry.Type = value
				continue
			}

			entry.Attrs[key] = value
		}

		if entry.Type == "" {
			entry.Type = "registry"
		}
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return nil, nil
	}

	return entries, nil
}

func normalizeEntitlementsInternal(entitlements []string, privileged bool) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(entitlements)+1)

	appendEntitlement := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	for _, entitlement := range entitlements {
		appendEntitlement(entitlement)
	}
	if privileged {
		appendEntitlement("security.insecure")
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func (b *Service) buildSolveOptInternal(ctx context.Context, req types.BuildRequest, providerName string) (buildkit.SolveOpt, <-chan error, func(), error) {
	fsInput, err := prepareBuildFilesystemInputInternal(req)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}

	cacheImports, err := parseBuildkitCacheEntriesInternal(req.CacheFrom)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}
	cacheExports, err := parseBuildkitCacheEntriesInternal(req.CacheTo)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}

	contextDir, dockerfilePath, cleanup, err := prepareBuildContextInternal(fsInput)
	if err != nil {
		return buildkit.SolveOpt{}, nil, nil, err
	}

	dockerfileDir := contextDir
	dockerfileFilename := dockerfilePath
	if dockerfileRelDir := filepath.Dir(filepath.FromSlash(dockerfilePath)); dockerfileRelDir != "." {
		dockerfileDir = filepath.Join(contextDir, dockerfileRelDir)
		dockerfileFilename = filepath.Base(dockerfilePath)
	}

	frontendAttrs := map[string]string{
		"filename": dockerfileFilename,
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
	dockerfileMount, err := fsutil.NewFS(dockerfileDir)
	if err != nil {
		cleanup()
		return buildkit.SolveOpt{}, nil, nil, fmt.Errorf("failed to prepare Dockerfile mount: %w", err)
	}

	solveOpt := buildkit.SolveOpt{
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
		LocalMounts: map[string]fsutil.FS{
			"context":    contextMount,
			"dockerfile": dockerfileMount,
		},
		CacheImports:        cacheImports,
		CacheExports:        cacheExports,
		AllowedEntitlements: normalizeEntitlementsInternal(req.Entitlements, req.Privileged),
	}

	var loadErrCh chan error
	exports := make([]buildkit.ExportEntry, 0, 2)
	if req.Push && providerName != "local" {
		exports = append(exports, buildkit.ExportEntry{
			Type: "image",
			Attrs: map[string]string{
				"name":           strings.Join(req.Tags, ","),
				"push":           "true",
				"oci-mediatypes": "true",
			},
		})
	}

	if providerName == "local" && (req.Load || req.Push) {
		exports = append(exports, buildkit.ExportEntry{
			Type:  "moby",
			Attrs: map[string]string{"name": strings.Join(req.Tags, ",")},
		})
	} else if req.Load {
		exportEntry, errCh, err := b.buildLoadExportInternal(ctx, req.Tags)
		if err != nil {
			cleanup()
			return buildkit.SolveOpt{}, nil, nil, err
		}
		loadErrCh = errCh
		exports = append(exports, exportEntry)
	}

	if len(exports) > 0 {
		solveOpt.Exports = exports
	}

	return solveOpt, loadErrCh, cleanup, nil
}

func (b *Service) buildLoadExportInternal(ctx context.Context, tags []string) (buildkit.ExportEntry, chan error, error) {
	if b.dockerClientProvider == nil {
		return buildkit.ExportEntry{}, nil, &types.BuildDockerServiceUnavailableError{}
	}

	dockerClient, err := b.dockerClientProvider.GetClient(ctx)
	if err != nil {
		return buildkit.ExportEntry{}, nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	pr, pw := io.Pipe()
	loadErrCh := make(chan error, 1)
	go func() {
		defer pr.Close()
		loadResp, loadErr := dockerClient.ImageLoad(ctx, pr)
		if loadErr != nil {
			loadErrCh <- loadErr
			return
		}
		defer func() { _ = loadResp.Close() }()
		loadErrCh <- dockerutils.RenderJSONMessageStream(loadResp, io.Discard)
	}()

	exportAttrs := map[string]string{}
	if len(tags) > 0 {
		exportAttrs["name"] = strings.Join(tags, ",")
	}

	return buildkit.ExportEntry{
		Type:  "docker",
		Attrs: exportAttrs,
		Output: func(_ map[string]string) (io.WriteCloser, error) {
			return pw, nil
		},
	}, loadErrCh, nil
}
