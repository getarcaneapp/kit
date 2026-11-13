package updater

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"kit.getarcane.app"
	"kit.getarcane.app/docker/compat"

	"kit.getarcane.app/updater/digest"
	"kit.getarcane.app/updater/internal/digestcheck"
	"kit.getarcane.app/updater/pkg/utils/tagpolicy"
	"kit.getarcane.app/updater/refs"
	"kit.getarcane.app/updater/types"
)

// CheckImageUpdate discovers an image update without pulling or changing state.
// CurrentDigest should describe the image in use, not a separately cached tag.
func (s *Service) CheckImageUpdate(ctx context.Context, request types.CheckRequest) (types.CheckResult, error) {
	result, err := s.selectImageTag(ctx, "", request.ImageRef, request.Policy)
	if err != nil || result.Reason != "" || result.UpdateAvailable {
		return result, err
	}
	digestResult := func(currentDigest, targetDigest string, updateAvailable bool) types.CheckResult {
		return types.CheckResult{
			CurrentRef:      result.CurrentRef,
			TargetRef:       result.TargetRef,
			CurrentVersion:  result.CurrentVersion,
			TargetVersion:   result.TargetVersion,
			CurrentDigest:   currentDigest,
			TargetDigest:    targetDigest,
			UpdateType:      string(UpdateTypeDigest),
			UpdateAvailable: updateAvailable,
		}
	}
	if request.CurrentDigest != "" {
		currentDigest, normalizeErr := digest.Normalize(request.CurrentDigest)
		if normalizeErr != nil {
			return digestResult(currentDigest, "", false), normalizeErr
		}
		remote, resolveErr := s.config.RegistryDigestResolver.ImageDigest(ctx, result.CurrentRef)
		if resolveErr != nil {
			return digestResult(currentDigest, "", false), resolveErr
		}
		targetDigest, targetErr := digest.Normalize(remote)
		if targetErr != nil {
			return digestResult(currentDigest, targetDigest, false), targetErr
		}
		return digestResult(currentDigest, targetDigest, currentDigest != targetDigest), nil
	}
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return digestResult("", "", false), err
	}
	check := digestcheck.NewChecker(dockerClient, s.config.RegistryDigestResolver).CheckImageNeedsUpdate(ctx, result.CurrentRef)
	return digestResult(check.LocalDigest, check.RemoteDigest, check.Error == nil && check.NeedsUpdate), check.Error
}

func (s *Service) selectImageTag(ctx context.Context, containerID, imageRef string, policy types.Policy) (types.CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return types.CheckResult{
			ContainerID: containerID,
			CurrentRef:  imageRef,
			TargetRef:   imageRef,
		}, err
	}
	if refs.IsDigestPinnedReference(imageRef) || refs.IsImageIDLikeReference(imageRef) {
		return types.CheckResult{
			ContainerID: containerID,
			CurrentRef:  imageRef,
			TargetRef:   imageRef,
			Reason:      "immutable image reference",
		}, nil
	}
	parsed, err := refs.NormalizeReference(imageRef)
	if err != nil {
		return types.CheckResult{
			ContainerID: containerID,
			CurrentRef:  imageRef,
			TargetRef:   imageRef,
		}, err
	}
	unchanged := types.CheckResult{
		ContainerID: containerID,
		CurrentRef:  parsed.NormalizedRef,
		TargetRef:   parsed.NormalizedRef,
	}
	policy, err = tagpolicy.Resolve(imageRef, policy)
	if err != nil {
		return unchanged, err
	}
	if policy.Strategy == "digest" {
		return unchanged, nil
	}
	tags, err := s.config.RegistryTagLister.ListTags(ctx, parsed.NormalizedRef)
	if err != nil {
		return unchanged, fmt.Errorf("list image tags: %w", err)
	}
	selected, err := tagpolicy.Select(parsed.Tag, tags, policy)
	if err != nil {
		return unchanged, err
	}
	currentVersion, err := tagpolicy.Version(parsed.Tag, policy)
	if err != nil {
		return unchanged, err
	}
	targetVersion, err := tagpolicy.Version(selected, policy)
	if err != nil {
		return types.CheckResult{
			ContainerID:    containerID,
			CurrentRef:     parsed.NormalizedRef,
			TargetRef:      parsed.NormalizedRef,
			CurrentVersion: currentVersion,
		}, err
	}
	return types.CheckResult{
		ContainerID:     containerID,
		CurrentRef:      parsed.NormalizedRef,
		TargetRef:       parsed.RegistryHost + "/" + parsed.Repository + ":" + selected,
		CurrentVersion:  currentVersion,
		TargetVersion:   targetVersion,
		UpdateType:      kit.Ternary(selected != parsed.Tag, string(UpdateTypeTag), ""),
		UpdateAvailable: selected != parsed.Tag,
	}, nil
}

// CheckContainerUpdate inspects one container and returns its current policy's
// candidate. It does not write pending records or change Docker resources.
func (s *Service) CheckContainerUpdate(ctx context.Context, containerID string) (types.CheckResult, error) {
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return types.CheckResult{}, err
	}
	inspected, err := compat.ContainerInspectWithCompatibility(ctx, dockerClient, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return types.CheckResult{}, err
	}
	inspect := inspected.Container
	excluded, err := s.excludedContainerSet(ctx, enforceSettingsExclusions)
	if err != nil {
		return types.CheckResult{ContainerID: cmp.Or(inspect.ID, containerID)}, err
	}
	if reason := s.containerEligibility(inspect, excluded, enforceSettingsExclusions); reason != "" {
		return types.CheckResult{ContainerID: cmp.Or(inspect.ID, containerID), Reason: reason}, nil
	}
	result, err := s.selectImageTag(ctx, inspect.ID, inspect.Config.Image, s.config.LabelPolicy.TagPolicy(inspect.Config.Labels))
	if err != nil || result.Reason != "" || result.UpdateAvailable {
		return result, err
	}
	// Compare the running image, even if the configured tag is cached at a newer digest.
	image, err := dockerClient.ImageInspect(ctx, inspect.Image)
	if err != nil {
		return result, err
	}
	remote, err := s.config.RegistryDigestResolver.ImageDigest(ctx, result.CurrentRef)
	if err != nil {
		return result, err
	}
	targetDigest, err := digest.Normalize(remote)
	if err != nil {
		return result, err
	}
	localDigests := digestcheck.RepoDigests(image.RepoDigests, result.CurrentRef)
	return types.CheckResult{
		ContainerID:    result.ContainerID,
		CurrentRef:     result.CurrentRef,
		TargetRef:      result.TargetRef,
		CurrentVersion: result.CurrentVersion,
		TargetVersion:  result.TargetVersion,
		CurrentDigest: func() string {
			switch {
			case slices.Contains(localDigests, targetDigest):
				return targetDigest
			case len(localDigests) > 0:
				return localDigests[0]
			}
			return image.ID
		}(),
		TargetDigest:    targetDigest,
		UpdateType:      string(UpdateTypeDigest),
		UpdateAvailable: !slices.Contains(localDigests, targetDigest),
	}, nil
}

// containerEligibility reports why a container cannot be updated, or "" when it can; policy decides
// whether the updater opt-out label and the excluded set, from excludedContainerSet, apply on top of the
// Swarm and config checks.
func (s *Service) containerEligibility(inspect container.InspectResponse, excluded map[string]bool, policy exclusionPolicy) string {
	if inspect.Config == nil {
		return "container config unavailable"
	}
	labels := inspect.Config.Labels
	if s.config.LabelPolicy.IsSwarmTask(labels) && !s.isSelfUpdateCandidate(inspect.ID, labels) {
		return "swarm service; update at the service level"
	}
	if reason := s.config.LabelPolicy.UnmanagedReason(labels); reason != "" {
		return reason
	}
	if policy == ignoreSettingsExclusions {
		return ""
	}
	if s.config.LabelPolicy.IsUpdateDisabled(labels) {
		return "updates disabled by label"
	}
	if isExcluded(excluded, inspect.ID, inspect.Name) {
		return "container excluded by settings"
	}
	return ""
}
