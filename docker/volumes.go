package docker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/samber/hot"
	"github.com/samber/mo"
	kit "go.getarcane.app/kit/pkg"
)

var volumeUsageCache = hot.NewHotCache[string, []volume.Volume](hot.LRU, 16).
	WithTTL(volumeUsageCacheTTL).
	WithCopyOnRead(cloneVolumeUsage).
	WithCopyOnWrite(cloneVolumeUsage).
	Build()

const (
	volumeUsageCacheTTL               = 30 * time.Second
	volumeUsageRefreshFallbackTimeout = 30 * time.Second
)

// GetVolumeUsageData returns current volume usage data, sharing an in-flight
// refresh with concurrent callers while allowing each caller to honor its own context.
func GetVolumeUsageData(ctx context.Context, dockerClient *client.Client) ([]volume.Volume, error) {
	if dockerClient == nil {
		return nil, errors.New("failed to get disk usage: Docker client is nil")
	}

	key := volumeUsageCacheKey(dockerClient)
	stale, staleFound := volumeUsageCache.Peek(key)
	if cached, found, _ := volumeUsageCache.Get(key); found {
		slog.DebugContext(ctx, "returning cached volume usage data", "volumeCount", len(cached), "dockerHost", key)
		return cached, nil
	}

	type refreshResult struct {
		volumes []volume.Volume
		found   bool
		err     error
	}
	result := make(chan refreshResult, 1)
	deadline, hasDeadline := ctx.Deadline()
	refreshCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), kit.Ternary(hasDeadline, deadline, time.Now().Add(volumeUsageRefreshFallbackTimeout)))
	go func() {
		defer cancel()
		volumes, found, err := volumeUsageCache.GetWithLoaders(key, func(_ []string) (map[string][]volume.Volume, error) {
			loaded, loadErr := FetchVolumeUsageData(refreshCtx, dockerClient)
			if loadErr != nil {
				return nil, loadErr
			}
			return map[string][]volume.Volume{key: loaded}, nil
		})
		result <- refreshResult{
			volumes: volumes,
			found:   found,
			err:     err,
		}
	}()

	select {
	case <-ctx.Done():
		if staleFound {
			slog.WarnContext(ctx, "volume usage refresh timed out; returning stale cache", "error", ctx.Err(), "volumeCount", len(stale), "dockerHost", key)
			return stale, nil
		}
		return nil, ctx.Err()
	case refreshed := <-result:
		if refreshed.err != nil {
			if staleFound {
				slog.WarnContext(ctx, "volume usage refresh failed; returning stale cache", "error", refreshed.err, "volumeCount", len(stale), "dockerHost", key)
				return stale, nil
			}
			return nil, refreshed.err
		}
		if !refreshed.found {
			return nil, errors.New("volume usage cache loader returned no data")
		}
		return refreshed.volumes, nil
	}
}

// GetVolumeUsageDataStaleWhileRevalidate returns immediately with any cached
// snapshot and starts a bounded refresh when the snapshot is stale or missing.
func GetVolumeUsageDataStaleWhileRevalidate(ctx context.Context, dockerClient *client.Client) mo.Option[[]volume.Volume] {
	if dockerClient == nil {
		return mo.None[[]volume.Volume]()
	}

	key := volumeUsageCacheKey(dockerClient)
	cached, found := volumeUsageCache.Peek(key)
	if fresh, freshFound, _ := volumeUsageCache.Get(key); freshFound {
		slog.DebugContext(ctx, "volume usage cache lookup",
			"dockerHost", key,
			"cacheState", "fresh",
			"volumeCount", len(fresh),
			"refreshRequested", false,
		)
		return mo.Some(fresh)
	}
	slog.DebugContext(ctx, "volume usage cache lookup",
		"dockerHost", key,
		"cacheState", kit.Ternary(found, "stale", "miss"),
		"volumeCount", len(cached),
		"refreshRequested", true,
	)
	deadline, hasDeadline := ctx.Deadline()
	refreshCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), kit.Ternary(hasDeadline, deadline, time.Now().Add(volumeUsageRefreshFallbackTimeout)))
	go func() {
		defer cancel()
		_, _, err := volumeUsageCache.GetWithLoaders(key, func(_ []string) (map[string][]volume.Volume, error) {
			loaded, loadErr := FetchVolumeUsageData(refreshCtx, dockerClient)
			if loadErr != nil {
				return nil, loadErr
			}
			return map[string][]volume.Volume{key: loaded}, nil
		})
		if err != nil {
			slog.WarnContext(refreshCtx, "failed to refresh volume usage cache", "error", err, "dockerHost", key)
		}
	}()
	if !found {
		return mo.None[[]volume.Volume]()
	}
	return mo.Some(cached)
}

// InvalidateVolumeUsageCache invalidates usage data for the Docker daemon used by dockerClient.
func InvalidateVolumeUsageCache(dockerClient *client.Client) {
	if dockerClient == nil {
		return
	}

	volumeUsageCache.Delete(volumeUsageCacheKey(dockerClient))
}

func volumeUsageCacheKey(dockerClient *client.Client) string {
	return cmp.Or(dockerClient.DaemonHost(), fmt.Sprintf("client:%p", dockerClient))
}

func cloneVolumeUsage(volumes []volume.Volume) []volume.Volume {
	return append([]volume.Volume(nil), volumes...)
}

// FetchVolumeUsageData queries the daemon for volume usage, bypassing the cache.
func FetchVolumeUsageData(ctx context.Context, dockerClient *client.Client) ([]volume.Volume, error) {
	diskUsage, err := dockerClient.DiskUsage(ctx, client.DiskUsageOptions{
		Volumes: true,
		Verbose: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get disk usage: %w", err)
	}

	slog.DebugContext(ctx, "disk usage returned volumes", "volumeCount", len(diskUsage.Volumes.Items))
	return append([]volume.Volume{}, diskUsage.Volumes.Items...), nil
}

// FilterContainersUsingVolume returns the IDs of containers in the provided slice that mount the named volume.
// Use this when checking many volumes in a row — list containers once and reuse the slice.
func FilterContainersUsingVolume(containers []container.Summary, volumeName string) []string {
	containerIDs := make([]string, 0)
	for _, c := range containers {
		if slices.ContainsFunc(c.Mounts, func(m container.MountPoint) bool {
			return m.Type == mount.TypeVolume && m.Name == volumeName
		}) {
			containerIDs = append(containerIDs, c.ID)
		}
	}
	return containerIDs
}

// GetContainersUsingVolume lists all containers and returns IDs that mount the named volume.
// For batch checks (multiple volumes), prefer listing containers once via dockerClient.ContainerList
// and calling FilterContainersUsingVolume per volume.
func GetContainersUsingVolume(ctx context.Context, dockerClient *client.Client, volumeName string) ([]string, error) {
	containerList, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	containerIDs := FilterContainersUsingVolume(containerList.Items, volumeName)
	slog.DebugContext(ctx, "found containers using volume", "volume", volumeName, "containerCount", len(containerIDs))
	return containerIDs, nil
}
