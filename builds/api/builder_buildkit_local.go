package api

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	buildkitclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	docker "go.getarcane.app/builds/pkg/docker"
	"go.getarcane.app/builds/types"
)

func (b *Service) newLocalBuildkitSessionInternal(ctx context.Context) (*buildSession, error) {
	if b.dockerClientProvider == nil {
		return nil, &types.BuildDockerServiceUnavailableError{}
	}

	dockerClient, err := b.dockerClientProvider.GetClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	bk, err := buildkitclient.New(ctx, "", docker.ClientOpts(dockerClient)...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker BuildKit: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := bk.Wait(waitCtx); err != nil {
		_ = bk.Close()
		return nil, fmt.Errorf("failed to wait for Docker BuildKit: %w", err)
	}

	return &buildSession{
		Client: bk,
		Close: func(_ error) error {
			return bk.Close()
		},
	}, nil
}

func requiresDirectLocalBuildkitSessionInternal(req types.BuildRequest) (bool, error) {
	fsInput, err := prepareBuildFilesystemInputInternal(req)
	if err != nil {
		return false, err
	}

	contents, err := readDockerfileContentsInternal(fsInput)
	if err != nil {
		return false, err
	}

	return dockerfileRequiresDirectBuildkitSessionInternal(contents)
}

func readDockerfileContentsInternal(input buildFilesystemInput) (string, error) {
	if input.dockerfileInline != "" {
		return input.dockerfileInline, nil
	}

	raw, err := os.ReadFile(input.fullDockerfilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read Dockerfile: %w", err)
	}

	return string(raw), nil
}

// dockerfileRequiresDirectBuildkitSessionInternal reports whether the Dockerfile
// declares a syntax frontend or uses BuildKit-only instruction flags that the
// Docker Engine build API does not accept.
func dockerfileRequiresDirectBuildkitSessionInternal(contents string) (bool, error) {
	if _, _, _, ok := parser.DetectSyntax([]byte(contents)); ok {
		return true, nil
	}

	result, err := parser.Parse(strings.NewReader(contents))
	if err != nil {
		return false, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	for _, node := range result.AST.Children {
		var buildkitFlags []string
		switch strings.ToLower(node.Value) {
		case "run":
			buildkitFlags = []string{"--mount", "--network", "--security"}
		case "copy", "add":
			buildkitFlags = []string{"--link"}
		default:
			continue
		}
		for _, flag := range node.Flags {
			name, _, _ := strings.Cut(flag, "=")
			if slices.Contains(buildkitFlags, name) {
				return true, nil
			}
		}
	}

	return false, nil
}
