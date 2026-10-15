package compat

import (
	"context"
	"maps"
	"slices"
	"strings"

	containertypes "github.com/moby/moby/api/types/container"
	systemtypes "github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

// EngineCompatibilityInfo describes the container engine details that decide
// whether recreate-time HostConfig sanitization is required.
type EngineCompatibilityInfo struct {
	// Name is the normalized engine identifier, "docker" or "podman".
	Name string
	// CgroupVersion is the daemon-reported cgroup version, such as "1" or "2".
	CgroupVersion string
}

// DetectEngineCompatibility derives the engine name from the daemon's version
// and info responses, checking the platform, components, and OS markers in order.
func DetectEngineCompatibility(version client.ServerVersionResult, info systemtypes.Info) EngineCompatibilityInfo {
	candidates := []string{version.Platform.Name}
	for _, component := range version.Components {
		candidates = append(candidates, component.Name)
		candidates = slices.AppendSeq(candidates, maps.Values(component.Details))
	}
	candidates = append(candidates, info.ServerVersion, info.OperatingSystem)
	return EngineCompatibilityInfo{
		Name: func() string {
			for _, candidate := range candidates {
				switch lower := strings.ToLower(candidate); {
				case strings.Contains(lower, "podman"):
					return "podman"
				case strings.Contains(lower, "docker"):
					return "docker"
				}
			}
			return ""
		}(),
		CgroupVersion: strings.TrimSpace(info.CgroupVersion),
	}
}

// SanitizeRecreateHostConfig removes recreate options the engine rejects:
// Podman on cgroup v2 refuses MemorySwappiness. It reports whether anything
// was removed.
func (e EngineCompatibilityInfo) SanitizeRecreateHostConfig(hostConfig *containertypes.HostConfig) bool {
	if hostConfig == nil || !strings.EqualFold(strings.TrimSpace(e.Name), "podman") ||
		strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e.CgroupVersion)), "v") != "2" || hostConfig.MemorySwappiness == nil {
		return false
	}
	hostConfig.MemorySwappiness = nil // keep: exported contract mutates hostConfig
	return true
}

// PrepareRecreateHostConfigForEngine shallow-copies hostConfig and removes the
// options the connected engine rejects, returning the copy, whether anything
// was removed, and the engine details behind that decision.
func PrepareRecreateHostConfigForEngine(ctx context.Context, dockerClient client.APIClient, hostConfig *containertypes.HostConfig) (*containertypes.HostConfig, bool, EngineCompatibilityInfo, error) {
	if hostConfig == nil {
		return nil, false, EngineCompatibilityInfo{}, nil
	}
	cloned := new(*hostConfig)
	serverVersion, err := dockerClient.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return cloned, false, EngineCompatibilityInfo{}, err
	}
	infoResult, err := dockerClient.Info(ctx, client.InfoOptions{})
	if err != nil {
		return cloned, false, EngineCompatibilityInfo{}, err
	}
	engine := DetectEngineCompatibility(serverVersion, infoResult.Info)
	return cloned, engine.SanitizeRecreateHostConfig(cloned), engine, nil
}
