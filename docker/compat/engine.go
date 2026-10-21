package compat

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
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
func PrepareRecreateHostConfigForEngine(
	ctx context.Context,
	dockerClient client.APIClient,
	containerID string,
	hostConfig *containertypes.HostConfig,
) (*containertypes.HostConfig, bool, EngineCompatibilityInfo, error) {
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
	sanitized := engine.SanitizeRecreateHostConfig(cloned)
	if engine.Name != "podman" || cloned.UsernsMode != "private" {
		return cloned, sanitized, engine, nil
	}
	raw, err := rawGet(ctx, dockerClient, path.Join("/v4.0.0/libpod/containers", strings.TrimSpace(containerID), "json"), nil)
	if err != nil {
		return cloned, sanitized, engine, fmt.Errorf("inspect podman user namespace of %s: %w", containerID, err)
	}
	var inspect struct {
		HostConfig struct {
			IDMappings struct {
				UIDMap []string `json:"UidMap"`
				GIDMap []string `json:"GidMap"`
			}
		}
	}
	if err = json.Unmarshal(raw, &inspect); err != nil {
		return cloned, sanitized, engine, fmt.Errorf("decode podman user namespace of %s: %w", containerID, err)
	}
	usernsMode, err := keepIDUsernsMode(inspect.HostConfig.IDMappings.UIDMap, inspect.HostConfig.IDMappings.GIDMap)
	if err != nil {
		return cloned, sanitized, engine, fmt.Errorf("recreate podman user namespace of %s: %w", containerID, err)
	}
	cloned.UsernsMode = usernsMode
	return cloned, sanitized, engine, nil
}

func keepIDUsernsMode(uidMap, gidMap []string) (containertypes.UsernsMode, error) {
	keepID := func(kind string, entries []string) (uint64, error) {
		for _, entry := range entries {
			fields := strings.Split(strings.TrimSpace(entry), ":")
			if len(fields) != 3 {
				return 0, fmt.Errorf("malformed %s mapping %q", kind, entry)
			}
			ids := make([]uint64, len(fields))
			for i, field := range fields {
				id, err := strconv.ParseUint(field, 10, 32)
				if err != nil {
					return 0, fmt.Errorf("malformed %s mapping %q: %w", kind, entry, err)
				}
				ids[i] = id
			}
			if ids[1] == 0 && ids[2] == 1 {
				return ids[0], nil
			}
		}
		return 0, fmt.Errorf("no keep-id %s mapping in %q; only keep-id user namespaces can be recreated", kind, entries)
	}
	uid, err := keepID("uid", uidMap)
	if err != nil {
		return "", err
	}
	gid, err := keepID("gid", gidMap)
	if err != nil {
		return "", err
	}
	return containertypes.UsernsMode(fmt.Sprintf("keep-id:uid=%d,gid=%d", uid, gid)), nil
}
