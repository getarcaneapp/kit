package updater

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/moby/moby/client"
	"go.getarcane.app/docker/compat"
	kit "go.getarcane.app/kit/pkg"

	"go.getarcane.app/updater/internal/digestcheck"
	"go.getarcane.app/updater/internal/match"
	"go.getarcane.app/updater/refs"
)

// ApplyPending applies pending image updates from the configured PendingStore.
func (s *Service) ApplyPending(ctx context.Context, opts Options) (out *Result, err error) {
	out, finish := newTimedResult()
	defer finish(&err)

	if s.config.PendingStore == nil {
		return nil, ErrPendingStoreRequired
	}
	records, err := s.config.PendingStore.PendingImageUpdates(ctx)
	if err != nil {
		return nil, fmt.Errorf("query pending image updates: %w", err)
	}
	if slices.ContainsFunc(records, func(record ImageUpdateRecord) bool { return record.IsTagUpdate() || record.ContainerID != "" }) {
		return out, s.applyTargetedRecords(ctx, records, opts, out)
	}
	if len(records) == 0 {
		return out, nil
	}
	usedImages, err := s.usedImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect used images: %w", err)
	}
	if len(usedImages) == 0 {
		return out, nil
	}

	dockerClient, dockerErr := s.dockerClient(ctx)
	digestChecker := digestcheck.NewChecker(dockerClient, s.config.RegistryDigestResolver)
	var plans []updatePlan
	for _, record := range records {
		oldRef := record.ImageRef()
		oldNorm := refs.NormalizeImageUpdateRef(oldRef)
		if !record.NeedsUpdate() || oldRef == "" {
			continue
		}
		if oldNorm == "" {
			s.logger.DebugContext(ctx, "skipping invalid pending image reference", "imageRef", oldRef)
			continue
		}
		if _, used := usedImages[oldNorm]; used {
			oldIDs, _ := digestChecker.GetImageIDsForRef(ctx, oldRef)
			plans = append(plans, updatePlan{record: record, oldRef: oldRef, newRef: record.NewImageRef(), oldIDs: match.AppendImageUpdateRecordIDToOldIDs(oldIDs, record.ID)})
		}
	}
	if len(plans) == 0 {
		return out, nil
	}
	if dockerErr != nil && !opts.DryRun {
		return nil, dockerErr
	}

	oldIDToNewRef := map[string]string{}
	oldRefToNewRef := map[string]string{}
	for i := range plans {
		plan := &plans[i]
		item := ResourceResult{
			ResourceID: plan.oldRef, ResourceType: ResourceTypeImage, ResourceName: plan.oldRef,
			Status: StatusSkipped, OldImage: plan.oldRef, NewImage: plan.newRef,
		}
		if !opts.DryRun {
			item.Status, item.Error = s.applyUpdatePlan(ctx, digestChecker, plan, opts.Force)
			item.UpdateApplied, item.UpdateAvailable = item.Status == StatusUpdated, item.Status == StatusUpdated
		}
		s.appendRecordedResult(ctx, out, item)
		if item.Status == StatusUpdated {
			for _, oldID := range plan.oldIDs {
				oldIDToNewRef[oldID] = plan.newRef
			}
			oldRefToNewRef[plan.oldRef] = plan.newRef
		}
	}

	// Containers still on a replaced image are restarted; a failed restart keeps its plan's record.
	var restarted []ResourceResult
	if len(oldIDToNewRef) > 0 || len(oldRefToNewRef) > 0 {
		restarted, err = s.RestartContainersUsingOldImages(ctx, oldIDToNewRef, oldRefToNewRef)
		for _, item := range restarted {
			s.appendRecordedResult(ctx, out, item)
		}
		if err != nil {
			return out, err
		}
	}
	for _, plan := range plans {
		newRef := refs.NormalizeImageUpdateRef(plan.newRef)
		restartFailed := slices.ContainsFunc(restarted, func(item ResourceResult) bool {
			return item.Status == StatusFailed && newRef != "" && refs.NormalizeImageUpdateRef(item.NewImage) == newRef
		})
		switch {
		case !plan.pulled:
		case restartFailed:
			s.logger.WarnContext(ctx, "keeping image update record after restart failure", "imageRef", plan.oldRef, "newRef", plan.newRef)
		default:
			if clearErr := s.config.PendingStore.ClearImageUpdateRecord(ctx, plan.record); clearErr != nil {
				s.logger.WarnContext(ctx, "failed to clear image update record", "imageRef", plan.oldRef, "error", clearErr)
			}
		}
	}
	return out, nil
}

// applyUpdatePlan pulls a plan's new reference unless it is already current, then reports whether any old
// image ID is still stale. It marks plan.pulled once the target image is present.
func (s *Service) applyUpdatePlan(ctx context.Context, digestChecker *digestcheck.Checker, plan *updatePlan, force bool) (ResourceStatus, string) {
	upToDate := false
	if !force {
		imageRef := refs.NormalizeImageUpdateRef(plan.newRef)
		if knownDigest := strings.TrimSpace(kit.FromPtr(plan.record.LatestDigest)); plan.record.IsDigestUpdate() && knownDigest != "" {
			check := digestChecker.CheckImageMatchesKnownDigest(ctx, imageRef, knownDigest)
			upToDate = check.Error == nil && !check.NeedsUpdate
		} else {
			check := digestChecker.CheckImageNeedsUpdate(ctx, imageRef)
			upToDate = check.CheckedViaAPI && check.Error == nil && !check.NeedsUpdate
		}
	}
	upToDateMessage := kit.Ternary(upToDate, "image already up to date", "image digest unchanged after pull")
	if !upToDate {
		if s.config.ImagePuller == nil {
			return StatusFailed, ErrImagePullerRequired.Error()
		}
		if err := s.config.ImagePuller.PullImage(ctx, plan.newRef, io.Discard); err != nil {
			return StatusFailed, fmt.Sprintf("pull failed: %v", err)
		}
	}
	plan.pulled = true

	if targetIDs, targetErr := digestChecker.GetImageIDsForRef(ctx, plan.newRef); targetErr == nil && !force {
		matched := slices.ContainsFunc(plan.oldIDs, func(id string) bool { return slices.Contains(targetIDs, id) })
		plan.oldIDs = slices.DeleteFunc(plan.oldIDs, func(id string) bool { return id == "" || slices.Contains(targetIDs, id) })
		if len(plan.oldIDs) == 0 && matched {
			return StatusUpToDate, upToDateMessage
		}
	}
	return StatusUpdated, ""
}

func (s *Service) dockerClient(ctx context.Context) (*client.Client, error) {
	if s.config.DockerClientProvider == nil {
		return nil, fmt.Errorf("docker connect: %w", ErrDockerClientProviderRequired)
	}
	dockerClient, err := s.config.DockerClientProvider.DockerClient(ctx)
	if err == nil && dockerClient == nil {
		err = ErrNilDockerClient
	}
	if err != nil {
		return nil, fmt.Errorf("docker connect: %w", err)
	}
	return dockerClient, nil
}

// usedImages returns the normalized references of images that running, eligible containers use.
func (s *Service) usedImages(ctx context.Context) (map[string]struct{}, error) {
	if s.config.UsedImageCollector != nil {
		return s.config.UsedImageCollector.UsedImages(ctx)
	}
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return nil, err
	}
	excluded, err := s.excludedContainerSet(ctx)
	if err != nil {
		return nil, err
	}
	listResult, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: false})
	if err != nil {
		return nil, err
	}

	policy := s.config.LabelPolicy
	out := map[string]struct{}{}
	for _, summary := range listResult.Items {
		if isExcluded(excluded, summary.ID, summary.Names...) || policy.IsUpdateDisabled(summary.Labels) ||
			(policy.IsSwarmTask(summary.Labels) && !policy.IsSelfUpdateTarget(summary.Labels)) {
			continue
		}
		if imageRef := refs.NormalizeImageUpdateRef(summary.Image); imageRef != "" {
			out[imageRef] = struct{}{}
			continue
		}
		inspectResult, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, summary.ID, client.ContainerInspectOptions{})
		if inspectErr != nil || inspectResult.Container.Config == nil || policy.IsUpdateDisabled(inspectResult.Container.Config.Labels) {
			continue
		}
		candidates := []string{inspectResult.Container.Config.Image}
		if imageInspect, imageErr := dockerClient.ImageInspect(ctx, inspectResult.Container.Image); imageErr == nil {
			candidates = append(candidates, imageInspect.RepoTags...)
		}
		for _, candidate := range candidates {
			if normalized := refs.NormalizeImageUpdateRef(candidate); normalized != "" {
				out[normalized] = struct{}{}
			}
		}
	}
	return out, nil
}

func (s *Service) excludedContainerSet(ctx context.Context) (map[string]bool, error) {
	excluded := map[string]bool{}
	if s.config.Settings == nil {
		return excluded, nil
	}
	names, err := s.config.Settings.ExcludedContainers(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range kit.TrimNonEmpty(names) {
		excluded[name] = true
	}
	return excluded, nil
}
