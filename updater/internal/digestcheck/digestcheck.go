// Package digestcheck compares local image digests with registry digests, so the updater can tell a
// real update from a no-op re-pull.
package digestcheck

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/moby/moby/client"

	"kit.getarcane.app"
	"kit.getarcane.app/cache"
	"kit.getarcane.app/updater/digest"
	"kit.getarcane.app/updater/refs"
)

type (
	// Resolver resolves a remote image digest without pulling. It is structural so
	// the root package's public resolver satisfies it without an import cycle.
	Resolver interface {
		ImageDigest(ctx context.Context, imageRef string) (string, error)
	}

	// CheckerConfig configures a digest Checker.
	CheckerConfig struct {
		DockerClient   *client.Client
		DigestResolver Resolver
		Logger         *slog.Logger
	}

	// Checker compares local image digests with remote digests.
	Checker struct {
		dockerClient   *client.Client
		digestResolver Resolver
		logger         *slog.Logger
	}

	// CheckResult contains the result of a digest check.
	CheckResult struct {
		Error         error
		LocalDigest   string
		RemoteDigest  string
		NeedsUpdate   bool
		CheckedViaAPI bool
	}

	// RefIDCache memoizes Checker.GetImageIDsForRef lookups by image reference.
	RefIDCache struct {
		checker *Checker
		ids     *cache.Cache[string, []string]
	}
)

// NewChecker creates a digest checker. Missing dependencies cause checks that
// need them to report an error; a nil Logger falls back to slog.Default().
func NewChecker(cfg CheckerConfig) *Checker {
	return &Checker{
		dockerClient:   cfg.DockerClient,
		digestResolver: cfg.DigestResolver,
		logger:         cmp.Or(cfg.Logger, slog.Default()),
	}
}

// RepoDigests returns the normalized digests in repoDigests that belong to
// imageRef's repository, ignoring digests the image carries for other repositories.
func RepoDigests(repoDigests []string, imageRef string) []string {
	target, err := refs.NormalizeReference(imageRef)
	if err != nil {
		return nil
	}
	var out []string
	for _, repoDigest := range repoDigests {
		name, _, _ := strings.Cut(repoDigest, "@")
		repo, repoErr := refs.NormalizeReference(name)
		normalized, ok := digest.FromReferenceSuffix(repoDigest)
		if repoErr == nil && ok && repo.RegistryHost == target.RegistryHost && repo.Repository == target.Repository {
			out = append(out, normalized)
		}
	}
	return out
}

// CheckImageNeedsUpdate compares local and remote digests for an image.
func (c *Checker) CheckImageNeedsUpdate(ctx context.Context, imageRef string) CheckResult {
	if pinnedDigest, ok := digest.FromReferenceSuffix(imageRef); ok {
		return CheckResult{LocalDigest: pinnedDigest, RemoteDigest: pinnedDigest}
	}
	if c == nil || c.dockerClient == nil {
		return CheckResult{Error: errors.New("docker client unavailable")}
	}

	c.logger.DebugContext(ctx, "CheckImageNeedsUpdate: checking image", "imageRef", imageRef, "normalizedRef", refs.NormalizeImageUpdateRef(imageRef))

	localDigests, err := c.localDigests(ctx, imageRef)
	if err != nil {
		return CheckResult{NeedsUpdate: true, Error: fmt.Errorf("image not found locally: %w", err)}
	}
	if len(localDigests) == 0 {
		return CheckResult{NeedsUpdate: true, Error: errors.New("no digest available for image")}
	}

	if c.digestResolver == nil {
		return CheckResult{LocalDigest: localDigests[0], Error: errors.New("remote digest resolver unavailable")}
	}

	remoteDigest, err := c.digestResolver.ImageDigest(ctx, imageRef)
	if err != nil {
		return CheckResult{LocalDigest: localDigests[0], Error: err}
	}

	return CheckResult{
		NeedsUpdate:   !slices.Contains(localDigests, remoteDigest),
		LocalDigest:   localDigests[0],
		RemoteDigest:  remoteDigest,
		CheckedViaAPI: true,
	}
}

// CheckImageMatchesKnownDigest reports whether the local image already carries
// a digest resolved earlier, avoiding a registry round-trip.
func (c *Checker) CheckImageMatchesKnownDigest(ctx context.Context, imageRef, knownDigest string) CheckResult {
	normalizedDigest, err := digest.Normalize(knownDigest)
	if err != nil {
		return CheckResult{Error: err}
	}

	if c == nil || c.dockerClient == nil {
		return CheckResult{RemoteDigest: normalizedDigest, Error: errors.New("docker client unavailable")}
	}

	localDigests, err := c.localDigests(ctx, imageRef)
	if err != nil {
		return CheckResult{
			NeedsUpdate:  true,
			RemoteDigest: normalizedDigest,
			Error:        err,
		}
	}

	matched := slices.Contains(localDigests, normalizedDigest)
	firstDigest := ""
	if len(localDigests) > 0 {
		firstDigest = localDigests[0]
	}
	return CheckResult{
		NeedsUpdate:  !matched,
		LocalDigest:  kit.Ternary(matched, normalizedDigest, firstDigest),
		RemoteDigest: normalizedDigest,
	}
}

func (c *Checker) localDigests(ctx context.Context, imageRef string) ([]string, error) {
	inspect, err := c.dockerClient.ImageInspect(ctx, imageRef)
	if err != nil {
		return nil, err
	}
	localDigests := RepoDigests(inspect.RepoDigests, imageRef)
	if len(localDigests) == 0 {
		if id := strings.TrimSpace(inspect.ID); id != "" {
			localDigests = []string{id}
		}
	}
	return localDigests, nil
}

// CompareWithPulled compares the current container image ID with a freshly pulled image.
func (c *Checker) CompareWithPulled(ctx context.Context, containerImageID, newImageRef string) (bool, error) {
	if c == nil || c.dockerClient == nil {
		return false, errors.New("docker client unavailable")
	}
	newInspect, err := c.dockerClient.ImageInspect(ctx, newImageRef)
	if err != nil {
		return false, fmt.Errorf("inspect new image: %w", err)
	}
	return strings.TrimSpace(containerImageID) != strings.TrimSpace(newInspect.ID), nil
}

// GetImageIDsForRef returns local image IDs associated with a reference.
func (c *Checker) GetImageIDsForRef(ctx context.Context, ref string) ([]string, error) {
	if c == nil || c.dockerClient == nil {
		return nil, errors.New("docker client unavailable")
	}

	inspect, err := c.dockerClient.ImageInspect(ctx, ref)
	if err == nil && strings.TrimSpace(inspect.ID) != "" {
		return []string{inspect.ID}, nil
	}

	imageList, err := c.dockerClient.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return nil, err
	}

	normalizedRef := refs.NormalizeImageUpdateRef(ref)
	var ids []string
	for _, img := range imageList.Items {
		if slices.ContainsFunc(img.RepoTags, func(tag string) bool { return refs.NormalizeImageUpdateRef(tag) == normalizedRef }) {
			ids = append(ids, img.ID)
		}
	}
	return ids, nil
}

// NewRefIDCache creates a memoizing image-ID lookup around checker.
func NewRefIDCache(checker *Checker) *RefIDCache {
	return &RefIDCache{
		checker: checker,
		// Never evict: repeated lookups for one ref must see one Docker snapshot.
		ids: &cache.Cache[string, []string]{},
	}
}

// IDsForRef returns the local image IDs for ref, caching results (including
// failed lookups, cached as nil) for the lifetime of the cache.
func (c *RefIDCache) IDsForRef(ctx context.Context, ref string) []string {
	if ids, ok := c.ids.Peek(ref); ok {
		return ids
	}
	ids, _ := c.checker.GetImageIDsForRef(ctx, ref)
	c.ids.Set(cache.Entry[string, []string]{
		Key:   ref,
		Value: ids,
	})
	return ids
}
