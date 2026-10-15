package api

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"

	"go.getarcane.app/builds/types"
)

func requiresDirectLocalBuildkitSession(req types.BuildRequest) (bool, error) {
	fsInput, err := prepareBuildFilesystemInput(req)
	if err != nil {
		return false, err
	}

	if fsInput.dockerfileInline != "" {
		return dockerfileRequiresDirectBuildkitSession(fsInput.dockerfileInline)
	}

	raw, err := os.ReadFile(fsInput.fullDockerfilePath)
	if err != nil {
		return false, fmt.Errorf("failed to read Dockerfile: %w", err)
	}

	return dockerfileRequiresDirectBuildkitSession(string(raw))
}

// dockerfileRequiresDirectBuildkitSession reports whether the Dockerfile
// declares a syntax frontend or uses BuildKit-only instruction flags that the
// Docker Engine build API does not accept.
func dockerfileRequiresDirectBuildkitSession(contents string) (bool, error) {
	if _, _, _, ok := parser.DetectSyntax([]byte(contents)); ok {
		return true, nil
	}

	result, err := parser.Parse(strings.NewReader(contents))
	if err != nil {
		return false, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	for _, node := range result.AST.Children {
		buildkitFlags, ok := map[string][]string{
			"run":  {"--mount", "--network", "--security"},
			"copy": {"--link"},
			"add":  {"--link"},
		}[strings.ToLower(node.Value)]
		if !ok {
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
