package updater

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.getarcane.app/updater/digest"
	"go.getarcane.app/updater/internal/compat"
	"go.getarcane.app/updater/internal/digestcheck"
	"go.getarcane.app/updater/pkg/utils/tagpolicy"
	"go.getarcane.app/updater/refs"
	"go.getarcane.app/updater/types"
)

// CheckImageUpdate discovers an image update without pulling or changing state.
// CurrentDigest should describe the image in use, not a separately cached tag.
func (s *Service) CheckImageUpdate(ctx context.Context, request types.CheckRequest) (types.CheckResult, error) {
	result, err := s.selectImageTagInternal(ctx, request.ImageRef, request.Policy)
	if err != nil || result.Reason != "" || result.UpdateAvailable {
		return result, err
	}
	result.UpdateType = string(UpdateTypeDigest)
	if request.CurrentDigest != "" {
		if s.config.RegistryDigestResolver == nil {
			return result, errors.New("remote digest resolver unavailable")
		}
		result.CurrentDigest, err = digest.Normalize(request.CurrentDigest)
		if err != nil {
			return result, err
		}
		remote, resolveErr := s.config.RegistryDigestResolver.ImageDigest(ctx, result.CurrentRef)
		if resolveErr != nil {
			return result, resolveErr
		}
		result.TargetDigest, err = digest.Normalize(remote)
		if err != nil {
			return result, err
		}
		result.UpdateAvailable = result.CurrentDigest != result.TargetDigest
		return result, nil
	}
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return result, err
	}
	check := digestcheck.NewChecker(dockerClient, s.config.RegistryDigestResolver).CheckImageNeedsUpdate(ctx, result.CurrentRef)
	result.CurrentDigest, result.TargetDigest = check.LocalDigest, check.RemoteDigest
	if check.Error != nil {
		return result, check.Error
	}
	result.UpdateAvailable = check.NeedsUpdate
	return result, nil
}

func (s *Service) selectImageTagInternal(ctx context.Context, imageRef string, policy types.Policy) (types.CheckResult, error) {
	result := types.CheckResult{CurrentRef: imageRef, TargetRef: imageRef}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if refs.IsDigestPinnedReference(imageRef) || refs.IsImageIDLikeReference(imageRef) {
		result.Reason = "immutable image reference"
		return result, nil
	}
	parsed, err := refs.NormalizeReference(imageRef)
	if err != nil {
		return result, err
	}
	result.CurrentRef, result.TargetRef = parsed.NormalizedRef, parsed.NormalizedRef
	policy, err = tagpolicy.Resolve(imageRef, policy)
	if err != nil {
		return result, err
	}
	if policy.Strategy == "digest" {
		return result, nil
	}
	if s.config.RegistryTagLister == nil {
		return result, errors.New("registry tag lister unavailable")
	}
	tags, err := s.config.RegistryTagLister.ListTags(ctx, parsed.NormalizedRef)
	if err != nil {
		return result, fmt.Errorf("list image tags: %w", err)
	}
	selected, err := tagpolicy.Select(parsed.Tag, tags, policy)
	if err != nil {
		return result, err
	}
	result.CurrentVersion, err = tagpolicy.Version(parsed.Tag, policy)
	if err != nil {
		return result, err
	}
	result.TargetVersion, err = tagpolicy.Version(selected, policy)
	if err != nil {
		return result, err
	}
	result.TargetRef = parsed.RegistryHost + "/" + parsed.Repository + ":" + selected
	if selected != parsed.Tag {
		result.UpdateType = string(UpdateTypeTag)
		result.UpdateAvailable = true
	}
	return result, nil
}

// CheckContainerUpdate inspects one container and returns its current policy's
// candidate. It does not write pending records or change Docker resources.
func (s *Service) CheckContainerUpdate(ctx context.Context, containerID string) (types.CheckResult, error) {
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return types.CheckResult{}, err
	}
	inspected, err := compat.ContainerInspect(ctx, dockerClient, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return types.CheckResult{}, err
	}
	inspect := inspected.Container
	result := types.CheckResult{ContainerID: inspect.ID}
	if result.ContainerID == "" {
		result.ContainerID = containerID
	}
	reason, err := s.containerEligibilityInternal(ctx, inspect)
	if err != nil {
		return result, err
	}
	if reason != "" {
		result.Reason = reason
		return result, nil
	}
	imageRef := inspect.Config.Image
	result, err = s.selectImageTagInternal(ctx, imageRef, s.config.LabelPolicy.TagPolicy(inspect.Config.Labels))
	result.ContainerID = inspect.ID
	if err != nil || result.Reason != "" || result.UpdateAvailable {
		return result, err
	}
	// Compare the running image, even if the configured tag is already cached at
	// a newer digest in the daemon.
	image, err := dockerClient.ImageInspect(ctx, inspect.Image)
	if err != nil {
		return result, err
	}
	if s.config.RegistryDigestResolver == nil {
		return result, errors.New("remote digest resolver unavailable")
	}
	remote, err := s.config.RegistryDigestResolver.ImageDigest(ctx, result.CurrentRef)
	if err != nil {
		return result, err
	}
	result.TargetDigest, err = digest.Normalize(remote)
	if err != nil {
		return result, err
	}
	result.CurrentDigest = image.ID
	result.UpdateType = string(UpdateTypeDigest)
	result.UpdateAvailable = true
	for _, repoDigest := range image.RepoDigests {
		if local, ok := digest.FromReferenceSuffix(repoDigest); ok {
			result.CurrentDigest = local
			if local == result.TargetDigest {
				result.UpdateAvailable = false
				break
			}
		}
	}
	return result, nil
}

func (s *Service) containerEligibilityInternal(ctx context.Context, inspect container.InspectResponse) (string, error) {
	if inspect.Config == nil {
		return "container config unavailable", nil
	}
	labels := inspect.Config.Labels
	if s.config.LabelPolicy.IsUpdateDisabled(labels) {
		return "updates disabled by label", nil
	}
	if s.config.LabelPolicy.IsSwarmTask(labels) && !s.isSelfUpdateCandidate(inspect.ID, labels) {
		return "swarm service; update at the service level", nil
	}
	excluded, err := s.excludedContainerSet(ctx)
	if err != nil {
		return "", err
	}
	if excluded[inspect.ID] || excluded[strings.TrimPrefix(inspect.Name, "/")] {
		return "container excluded by settings", nil
	}
	return "", nil
}
