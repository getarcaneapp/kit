// Package compat gates newer Docker daemon features behind the API version a
// daemon actually speaks, so container creation works against older daemons.
package compat

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	// NetworkScopedMacAddressMinAPIVersion is the first API version that accepts a
	// per-network MAC address in endpoint settings.
	NetworkScopedMacAddressMinAPIVersion = "1.44"

	// MultiEndpointContainerCreateMinAPIVersion is the first API version that
	// reliably accepts multiple NetworkingConfig.EndpointsConfig entries on
	// ContainerCreate. Older daemons need the container created on one network and
	// the rest attached with NetworkConnect before start, which the host Compose
	// CLI does on its own but in-process API callers must do themselves.
	MultiEndpointContainerCreateMinAPIVersion = "1.44"
)

// DetectDockerAPIVersion returns the client's negotiated API version, falling
// back to the daemon-reported version when the client has none yet.
func DetectDockerAPIVersion(ctx context.Context, dockerClient client.APIClient) string {
	if version := strings.TrimSpace(dockerClient.ClientVersion()); version != "" {
		return version
	}
	serverVersion, err := dockerClient.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(serverVersion.APIVersion)
}

// IsDockerAPIVersionAtLeast compares dotted API versions such as "1.43" and
// "1.44.1" numerically. It reports false when either side does not parse.
func IsDockerAPIVersionAtLeast(current, minimum string) bool {
	parse := func(version string) ([3]int, bool) {
		var parsed [3]int
		parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(version, "v")), ".")
		if len(parts) < 2 {
			return parsed, false
		}
		for i := range min(len(parts), len(parsed)) {
			n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
			if err != nil {
				return parsed, false
			}
			parsed[i] = n
		}
		return parsed, true
	}
	cur, okCurrent := parse(current)
	minV, okMinimum := parse(minimum)
	return okCurrent && okMinimum && slices.Compare(cur[:], minV[:]) >= 0
}

// SanitizeContainerCreateEndpointSettingsForDockerAPI deep-copies endpoint
// settings and drops per-network MAC addresses on daemons older than
// NetworkScopedMacAddressMinAPIVersion.
func SanitizeContainerCreateEndpointSettingsForDockerAPI(endpoints map[string]*network.EndpointSettings, apiVersion string) map[string]*network.EndpointSettings {
	if len(endpoints) == 0 {
		return nil
	}
	keepMAC := IsDockerAPIVersionAtLeast(apiVersion, NetworkScopedMacAddressMinAPIVersion)
	cloned := make(map[string]*network.EndpointSettings, len(endpoints))
	for name, endpoint := range endpoints {
		cloned[name] = endpoint.Copy()
		if cloned[name] != nil && !keepMAC {
			cloned[name].MacAddress = nil
		}
	}
	return cloned
}

// PrepareContainerCreateOptionsForDockerAPI keeps only the primary network in
// the create request on daemons older than
// MultiEndpointContainerCreateMinAPIVersion and returns the withheld endpoints
// for ConnectContainerExtraNetworksForDockerAPI. The primary network is the
// named NetworkMode when it has an endpoint, or the first endpoint by name
// when NetworkMode is unset; host, none, container, and unknown named modes
// leave the request untouched.
func PrepareContainerCreateOptionsForDockerAPI(options client.ContainerCreateOptions, apiVersion string) (client.ContainerCreateOptions, map[string]*network.EndpointSettings) {
	if IsDockerAPIVersionAtLeast(apiVersion, MultiEndpointContainerCreateMinAPIVersion) || options.NetworkingConfig == nil || len(options.NetworkingConfig.EndpointsConfig) <= 1 {
		return options, nil
	}
	endpoints := options.NetworkingConfig.EndpointsConfig

	primary := slices.Sorted(maps.Keys(endpoints))[0]
	if options.HostConfig != nil {
		if mode := container.NetworkMode(strings.TrimSpace(string(options.HostConfig.NetworkMode))); mode != "" {
			if _, ok := endpoints[string(mode)]; !ok || mode.IsHost() || mode.IsNone() || mode.IsContainer() {
				return options, nil
			}
			primary = string(mode)
		}
	}

	adjusted := options
	adjusted.HostConfig = &container.HostConfig{}
	if options.HostConfig != nil {
		adjusted.HostConfig = new(*options.HostConfig)
	}
	if strings.TrimSpace(string(adjusted.HostConfig.NetworkMode)) == "" {
		adjusted.HostConfig.NetworkMode = container.NetworkMode(primary)
	}
	adjusted.NetworkingConfig = &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{primary: endpoints[primary].Copy()},
	}

	extra := make(map[string]*network.EndpointSettings, len(endpoints)-1)
	for name, endpoint := range endpoints {
		if name != primary {
			extra[name] = endpoint.Copy()
		}
	}
	return adjusted, extra
}

// ConnectContainerExtraNetworksForDockerAPI attaches, in name order, the
// endpoints withheld from ContainerCreate. Call it after create and before start.
func ConnectContainerExtraNetworksForDockerAPI(ctx context.Context, dockerClient client.APIClient, containerID string, endpoints map[string]*network.EndpointSettings) error {
	if strings.TrimSpace(containerID) == "" {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(endpoints)) {
		_, err := dockerClient.NetworkConnect(ctx, name, client.NetworkConnectOptions{Container: containerID, EndpointConfig: endpoints[name].Copy()})
		if err != nil {
			return fmt.Errorf("connect network %s: %w", name, err)
		}
	}
	return nil
}

// ContainerCreateWithCompatibility detects the daemon API version and creates
// the container with the compatibility shims applied.
func ContainerCreateWithCompatibility(ctx context.Context, dockerClient client.APIClient, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	return ContainerCreateWithCompatibilityForAPIVersion(ctx, dockerClient, options, DetectDockerAPIVersion(ctx, dockerClient))
}

// ContainerCreateWithCompatibilityForAPIVersion creates the container for an
// already resolved API version, attaching withheld networks before returning
// and removing the container when an attachment fails. The removal ignores
// ctx cancellation, since an attachment that failed because ctx expired must
// still not leave the half-connected container behind.
func ContainerCreateWithCompatibilityForAPIVersion(ctx context.Context, dockerClient client.APIClient, options client.ContainerCreateOptions, apiVersion string) (client.ContainerCreateResult, error) {
	adjusted, extra := PrepareContainerCreateOptionsForDockerAPI(options, apiVersion)
	result, err := dockerClient.ContainerCreate(ctx, adjusted)
	if err != nil {
		return client.ContainerCreateResult{}, err
	}
	err = ConnectContainerExtraNetworksForDockerAPI(ctx, dockerClient, result.ID, extra)
	if err != nil {
		_, _ = dockerClient.ContainerRemove(context.WithoutCancel(ctx), result.ID, client.ContainerRemoveOptions{Force: true})
		return client.ContainerCreateResult{}, err
	}
	return result, nil
}
