package api

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	kit "go.getarcane.app/kit/pkg"
	"go.getarcane.app/kit/pkg/utils"

	"go.getarcane.app/builds/types"
)

func validateBuildRequest(req types.BuildRequest, providerName string) error {
	if strings.TrimSpace(req.ContextDir) == "" {
		return &types.BuildContextDirRequiredError{}
	}

	contextDir := filepath.Clean(req.ContextDir)
	if _, err := os.Stat(contextDir); err != nil {
		return fmt.Errorf("build context not found: %w", err)
	}

	if strings.TrimSpace(req.Dockerfile) != "" && strings.TrimSpace(req.DockerfileInline) != "" {
		return &types.DockerfileAndInlineMutuallyExclusiveError{}
	}

	if providerName == "depot" && !req.Push {
		return &types.DepotBuildPushRequiredError{}
	}

	unsupported := make([]string, 0, 5)
	switch providerName {
	case "local":
		if utils.HasNonEmpty(req.CacheTo) {
			unsupported = append(unsupported, "cacheTo")
		}
		if utils.HasNonEmpty(req.Entitlements) {
			unsupported = append(unsupported, "entitlements")
		}
		if req.Privileged {
			unsupported = append(unsupported, "privileged")
		}
		if utils.CountNonEmpty(req.Platforms) > 1 {
			unsupported = append(unsupported, "platforms")
		}
	case "depot":
		if strings.TrimSpace(req.Network) != "" {
			unsupported = append(unsupported, "network")
		}
		if strings.TrimSpace(req.Isolation) != "" {
			unsupported = append(unsupported, "isolation")
		}
		if req.ShmSize > 0 {
			unsupported = append(unsupported, "shmSize")
		}
		if len(req.Ulimits) > 0 {
			unsupported = append(unsupported, "ulimits")
		}
		if utils.HasNonEmpty(req.ExtraHosts) {
			unsupported = append(unsupported, "extraHosts")
		}
	}
	slices.Sort(unsupported)

	if len(unsupported) > 0 {
		return fmt.Errorf("unsupported build options for provider %s: %s", providerName, strings.Join(unsupported, ", "))
	}

	if len(req.Tags) == 0 && (req.Push || req.Load) {
		return &types.BuildTagsRequiredError{}
	}

	if strings.TrimSpace(req.DockerfileInline) != "" {
		return nil
	}
	dockerfilePath := cmp.Or(strings.TrimSpace(req.Dockerfile), "Dockerfile")
	if _, err := os.Stat(kit.Ternary(filepath.IsAbs(dockerfilePath), dockerfilePath, filepath.Join(contextDir, dockerfilePath))); err != nil {
		return fmt.Errorf("dockerfile not found: %w", err)
	}

	return nil
}
