// Package digestcheck compares local image digests with registry digests, so the updater can tell a
// real update from a no-op re-pull.
package digestcheck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"

	"github.com/moby/moby/client"
	"github.com/samber/hot"
	kit "go.getarcane.app/kit/pkg"

	"go.getarcane.app/updater/digest"
	"go.getarcane.app/updater/refs"
)

// Resolver resolves a remote image digest without pulling. It is structural so
// the root package's public resolver satisfies it without an import cycle.
type Resolver interface {
	ImageDigest(ctx context.Context, imageRef string) (string, error)
}

// Checker compares local image digests with remote digests.
type Checker struct {
	dockerClient   *client.Client
	digestResolver Resolver
}

// CheckResult contains the result of a digest check.
type CheckResult struct {
	NeedsUpdate   bool
	LocalDigest   string
	RemoteDigest  string
	Error         error
	CheckedViaAPI bool
}

// NewChecker creates a digest checker. Both arguments are optional; checks that
// need a missing one report an error.
func NewChecker(dockerClient *client.Client, digestResolver Resolver) *Checker {
	return &Checker{dockerClient: dockerClient, digestResolver: digestResolver}
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

	slog.DebugContext(ctx, "CheckImageNeedsUpdate: checking image", "imageRef", imageRef, "normalizedRef", refs.NormalizeImageUpdateRef(imageRef))

	inspect, err := c.dockerClient.ImageInspect(ctx, imageRef)
	if err != nil {
		return CheckResult{NeedsUpdate: true, Error: fmt.Errorf("image not found locally: %w", err)}
	}
	localDigests := RepoDigests(inspect.RepoDigests, imageRef)
	if len(localDigests) == 0 && inspect.ID != "" {
		localDigests = []string{inspect.ID}
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

	inspect, err := c.dockerClient.ImageInspect(ctx, imageRef)
	if err != nil {
		return CheckResult{
			NeedsUpdate:  true,
			RemoteDigest: normalizedDigest,
			Error:        err,
		}
	}

	localDigests := RepoDigests(inspect.RepoDigests, imageRef)
	if len(localDigests) == 0 {
		localDigests = []string{strings.TrimSpace(inspect.ID)}
	}
	return CheckResult{
		NeedsUpdate:  !slices.Contains(localDigests, normalizedDigest),
		LocalDigest:  kit.Ternary(slices.Contains(localDigests, normalizedDigest), normalizedDigest, localDigests[0]),
		RemoteDigest: normalizedDigest,
	}
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

// RefIDCache memoizes Checker.GetImageIDsForRef lookups by image reference.
type RefIDCache struct {
	checker *Checker
	ids     *hot.HotCache[string, []string]
}

// NewRefIDCache creates a memoizing image-ID lookup around checker.
func NewRefIDCache(checker *Checker) *RefIDCache {
	return &RefIDCache{
		checker: checker,
		// Never evict: repeated lookups for one ref must see one Docker snapshot.
		ids: hot.NewHotCache[string, []string](hot.LRU, math.MaxInt).
			WithoutLocking().
			Build(),
	}
}

// IDsForRef returns the local image IDs for ref, caching results (including
// failed lookups, cached as nil) for the lifetime of the cache.
func (c *RefIDCache) IDsForRef(ctx context.Context, ref string) []string {
	if ids, ok := c.ids.Peek(ref); ok {
		return ids
	}
	ids, _ := c.checker.GetImageIDsForRef(ctx, ref)
	c.ids.Set(ref, ids)
	return ids
}
