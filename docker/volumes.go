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
	"github.com/samber/mo"

	"kit.getarcane.app"
	"kit.getarcane.app/cache"
)

type (
	// VolumeUsageCacheConfig configures a VolumeUsageCache. Zero fields use defaults.
	VolumeUsageCacheConfig struct {
		Logger         *slog.Logger
		Capacity       int
		TTL            time.Duration
		RefreshTimeout time.Duration
	}

	// VolumeUsageCache caches Docker daemon volume usage snapshots per daemon host,
	// sharing in-flight refreshes across concurrent callers.
	VolumeUsageCache struct {
		cache          *cache.Cache[string, []volume.Volume]
		logger         *slog.Logger
		refreshTimeout time.Duration
	}
)

const (
	defaultVolumeUsageCacheCapacity   = 16
	volumeUsageCacheTTL               = 30 * time.Second
	volumeUsageRefreshFallbackTimeout = 30 * time.Second
)

// NewVolumeUsageCache constructs a VolumeUsageCache with the given configuration.
func NewVolumeUsageCache(cfg VolumeUsageCacheConfig) *VolumeUsageCache {
	return &VolumeUsageCache{
		cache: &cache.Cache[string, []volume.Volume]{
			Capacity: kit.Ternary(cfg.Capacity > 0, cfg.Capacity, defaultVolumeUsageCacheCapacity),
			TTL:      kit.Ternary(cfg.TTL > 0, cfg.TTL, volumeUsageCacheTTL),
			Clone:    slices.Clone[[]volume.Volume],
		},
		logger:         cmp.Or(cfg.Logger, slog.Default()),
		refreshTimeout: kit.Ternary(cfg.RefreshTimeout > 0, cfg.RefreshTimeout, volumeUsageRefreshFallbackTimeout),
	}
}

// Get returns current volume usage data, sharing an in-flight refresh with
// concurrent callers while allowing each caller to honor its own context.
func (c *VolumeUsageCache) Get(ctx context.Context, dockerClient *client.Client) ([]volume.Volume, error) {
	if dockerClient == nil {
		return nil, errors.New("failed to get disk usage: Docker client is nil")
	}

	key := volumeUsageCacheKey(dockerClient)
	stale, staleFound := c.cache.Peek(key)
	if cached, found, _ := c.cache.Get(key); found {
		c.logger.DebugContext(ctx, "returning cached volume usage data", "volumeCount", len(cached), "dockerHost", key)
		return cached, nil
	}

	type refreshResult struct {
		err     error
		volumes []volume.Volume
		found   bool
	}
	result := make(chan refreshResult, 1)
	refreshCtx, cancel := c.refreshContext(ctx)
	go func() {
		defer cancel()
		volumes, found, err := c.load(refreshCtx, dockerClient, key)
		result <- refreshResult{
			volumes: volumes,
			found:   found,
			err:     err,
		}
	}()

	select {
	case <-ctx.Done():
		if staleFound {
			c.logger.WarnContext(ctx, "volume usage refresh timed out; returning stale cache", "error", ctx.Err(), "volumeCount", len(stale), "dockerHost", key)
			return stale, nil
		}
		return nil, ctx.Err()
	case refreshed := <-result:
		if refreshed.err != nil {
			if staleFound {
				c.logger.WarnContext(ctx, "volume usage refresh failed; returning stale cache", "error", refreshed.err, "volumeCount", len(stale), "dockerHost", key)
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

// GetStaleWhileRevalidate returns immediately with any cached snapshot and
// starts a bounded refresh when the snapshot is stale or missing.
func (c *VolumeUsageCache) GetStaleWhileRevalidate(ctx context.Context, dockerClient *client.Client) mo.Option[[]volume.Volume] {
	if dockerClient == nil {
		return mo.None[[]volume.Volume]()
	}

	key := volumeUsageCacheKey(dockerClient)
	cached, found := c.cache.Peek(key)
	if fresh, freshFound, _ := c.cache.Get(key); freshFound {
		c.logger.DebugContext(ctx, "volume usage cache lookup",
			"dockerHost", key,
			"cacheState", "fresh",
			"volumeCount", len(fresh),
			"refreshRequested", false,
		)
		return mo.Some(fresh)
	}
	c.logger.DebugContext(ctx, "volume usage cache lookup",
		"dockerHost", key,
		"cacheState", kit.Ternary(found, "stale", "miss"),
		"volumeCount", len(cached),
		"refreshRequested", true,
	)
	refreshCtx, cancel := c.refreshContext(ctx)
	go func() {
		defer cancel()
		if _, _, err := c.load(refreshCtx, dockerClient, key); err != nil {
			c.logger.WarnContext(refreshCtx, "failed to refresh volume usage cache", "error", err, "dockerHost", key)
		}
	}()
	if !found {
		return mo.None[[]volume.Volume]()
	}
	return mo.Some(cached)
}

// Invalidate removes cached usage data for the Docker daemon used by dockerClient.
func (c *VolumeUsageCache) Invalidate(dockerClient *client.Client) {
	if dockerClient == nil {
		return
	}
	c.cache.Delete(volumeUsageCacheKey(dockerClient))
}

func (c *VolumeUsageCache) refreshContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, hasDeadline := ctx.Deadline()
	return context.WithDeadline(context.WithoutCancel(ctx), kit.Ternary(hasDeadline, deadline, time.Now().Add(c.refreshTimeout)))
}

func (c *VolumeUsageCache) load(refreshCtx context.Context, dockerClient *client.Client, key string) ([]volume.Volume, bool, error) {
	return c.cache.GetOrLoad(key, func(string) ([]volume.Volume, bool, error) {
		loaded, loadErr := FetchVolumeUsageData(refreshCtx, dockerClient)
		return loaded, loadErr == nil, loadErr
	})
}

func volumeUsageCacheKey(dockerClient *client.Client) string {
	return cmp.Or(dockerClient.DaemonHost(), fmt.Sprintf("client:%p", dockerClient))
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
	return slices.Clone(diskUsage.Volumes.Items), nil
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
