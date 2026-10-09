package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.getarcane.app/docker/compat"

	"go.getarcane.app/updater/internal/deps"
	"go.getarcane.app/updater/pkg/utils/tagpolicy"
	"go.getarcane.app/updater/refs"
)

type targetedRecord struct {
	record  ImageUpdateRecord
	targets []string
	failed  bool
}

// applyTargetedRecords applies container-scoped and tag records by recreating exactly the containers they
// target, and clears a record only once every one of its targets succeeded.
func (s *Service) applyTargetedRecords(ctx context.Context, records []ImageUpdateRecord, opts Options, out *Result) error {
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return err
	}
	listed, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return err
	}
	proxy := dockerProxyContainerName(dockerClient.DaemonHost())
	scan := &restartScan{plansByName: map[string]*restartPlan{}, markedForRestart: map[string]bool{}}
	states := make([]targetedRecord, 0, len(records))
	for _, record := range records {
		if !record.NeedsUpdate() {
			continue
		}
		state, collectErr := s.collectTargetRecord(ctx, dockerClient, listed.Items, proxy, record, scan, out)
		if collectErr != nil {
			return collectErr
		}
		states = append(states, state)
	}
	// All Compose conflicts and adapter capabilities must be checked before pulls.
	for _, group := range s.buildComposeGroups(ctx, s.sortRestartCandidates(ctx, scan), scan.plansByName) {
		if group.err != nil {
			return group.err
		}
	}

	results := s.pullTargetPlans(ctx, dockerClient, scan, opts)
	if !opts.DryRun && len(scan.markedForRestart) > 0 {
		for _, cnt := range listed.Items {
			name := containerSummaryName(cnt)
			if _, planned := scan.plansByName[name]; planned || cnt.State != container.StateRunning || name == proxy {
				continue
			}
			inspected, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
			if inspectErr != nil {
				continue
			}
			if reason, eligibilityErr := s.containerEligibility(ctx, inspected.Container, enforceSettingsExclusions); eligibilityErr != nil || reason != "" {
				continue
			}
			pinNetworkContainer(&inspected.Container, listed.Items)
			scan.plansByName[name] = &restartPlan{cnt: cnt, inspect: &inspected.Container}
			scan.containers = append(scan.containers, deps.ExtractContainerDeps(ctx, name, cnt, inspected.Container))
		}
		propagateImplicitRestarts(scan)
		restarted, restartErr := s.executeRestartPlans(ctx, dockerClient, s.sortRestartCandidates(ctx, scan), scan.plansByName)
		if restartErr != nil {
			return restartErr
		}
		results = append(results, restarted...)
	}

	successful, failed := map[string]bool{}, map[string]bool{}
	for _, result := range results {
		s.appendRecordedResult(ctx, out, result)
		failed[result.ResourceID] = failed[result.ResourceID] || result.Status == StatusFailed
		successful[result.ResourceID] = !failed[result.ResourceID] && (successful[result.ResourceID] || result.Status == StatusUpdated || result.Status == StatusUpToDate)
	}
	for _, state := range states {
		if opts.DryRun || state.failed || slices.ContainsFunc(state.targets, func(id string) bool { return !successful[id] }) {
			continue
		}
		if err = s.config.PendingStore.ClearImageUpdateRecord(ctx, state.record); err != nil {
			return fmt.Errorf("clear applied pending record: %w", err)
		}
	}
	return nil
}

// collectTargetRecord plans every running container record targets and reports the ones it must reject.
func (s *Service) collectTargetRecord(
	ctx context.Context,
	dockerClient *client.Client,
	containers []container.Summary,
	proxy string,
	record ImageUpdateRecord,
	scan *restartScan,
	out *Result,
) (targetedRecord, error) {
	state := targetedRecord{record: record}
	for _, cnt := range containers {
		if cnt.State != container.StateRunning || (record.ContainerID != "" && record.ContainerID != cnt.ID) {
			continue
		}
		name := containerSummaryName(cnt)
		inspected, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			// An unscoped record cannot safely be cleared after an incomplete scan.
			state.failed = true
			s.appendRecordedResult(ctx, out, failedContainerResult(cnt.ID, name, inspectErr.Error()))
			continue
		}
		inspect := inspected.Container
		if record.ContainerID == "" && (inspect.Config == nil || refs.NormalizeImageUpdateRef(inspect.Config.Image) != refs.NormalizeImageUpdateRef(record.ImageRef())) {
			continue
		}
		if proxy != "" && name == proxy {
			state.failed = true
			s.appendRecordedResult(ctx, out, skippedContainerResult(cnt.ID, proxy, "Docker proxy excluded from pending updates"))
			continue
		}
		state.targets = append(state.targets, cnt.ID)
		plan, reason, planErr := s.targetRecordPlan(ctx, cnt, inspect, record)
		if planErr != nil || reason != "" {
			state.failed = true
			if reason != "" {
				s.appendRecordedResult(ctx, out, skippedContainerResult(cnt.ID, name, reason))
			} else {
				s.appendRecordedResult(ctx, out, failedContainerResult(cnt.ID, name, planErr.Error()))
			}
			continue
		}
		if previous := scan.plansByName[name]; previous != nil {
			if previous.newRef != plan.newRef {
				return state, fmt.Errorf("conflicting image targets for container %s", cnt.ID)
			}
			continue
		}
		pinNetworkContainer(plan.inspect, containers)
		scan.plansByName[name] = plan
		scan.containers = append(scan.containers, deps.ExtractContainerDeps(ctx, name, cnt, *plan.inspect))
		scan.markedForRestart[name] = true
	}
	if len(state.targets) == 0 {
		state.failed = true
		s.appendRecordedResult(ctx, out, skippedContainerResult(record.ContainerID, record.ImageRef(), "pending update has no matching container"))
	}
	return state, nil
}

// targetRecordPlan validates that record still applies to the container under its current policy.
func (s *Service) targetRecordPlan(ctx context.Context, cnt container.Summary, inspect container.InspectResponse, record ImageUpdateRecord) (*restartPlan, string, error) {
	reason, err := s.containerEligibility(ctx, inspect, enforceSettingsExclusions)
	if err != nil || reason != "" {
		return nil, reason, err
	}
	current := inspect.Config.Image
	if refs.IsDigestPinnedReference(current) || refs.IsImageIDLikeReference(current) {
		return nil, "", errors.New("immutable image reference")
	}
	oldRef, newRef := refs.NormalizeImageUpdateRef(record.ImageRef()), refs.NormalizeImageUpdateRef(record.NewImageRef())
	if oldRef == "" || newRef == "" {
		return nil, "", errors.New("invalid pending image reference")
	}
	if current = refs.NormalizeImageUpdateRef(current); current != oldRef && current != newRef {
		return nil, "", errors.New("container image changed since update check")
	}
	if record.IsTagUpdate() {
		old, oldErr := refs.NormalizeReference(oldRef)
		next, nextErr := refs.NormalizeReference(newRef)
		if parseErr := errors.Join(oldErr, nextErr); parseErr != nil {
			return nil, "", parseErr
		}
		policy, policyErr := tagpolicy.Resolve(oldRef, s.config.LabelPolicy.TagPolicy(inspect.Config.Labels))
		if policyErr != nil {
			return nil, "", policyErr
		}
		if policy.Strategy != "tag" {
			return nil, "", errors.New("tag updates disabled by current policy")
		}
		selected, selectErr := tagpolicy.Select(old.Tag, []string{next.Tag}, policy)
		if selectErr != nil {
			return nil, "", selectErr
		}
		if selected != next.Tag || old.Tag == next.Tag {
			return nil, "", errors.New("pending tag is not a newer allowed version")
		}
	}
	if _, err = s.preflightComposeImage(ctx, cnt.ID, inspect, newRef); err != nil {
		return nil, "", err
	}
	return &restartPlan{cnt: cnt, inspect: &inspect, newRef: newRef, match: oldRef}, "", nil
}

// pullTargetPlans pulls each planned target once, in scan order for deterministic outcomes, and unplans
// failed and no-op targets so they do not seed dependency restarts.
func (s *Service) pullTargetPlans(ctx context.Context, dockerClient *client.Client, scan *restartScan, opts Options) []ResourceResult {
	pulled := map[string]error{}
	var results []ResourceResult
	for _, candidate := range scan.containers {
		plan := scan.plansByName[candidate.Name]
		result := standaloneRestartResult(candidate, plan)
		if opts.DryRun {
			result.Status, result.UpdateAvailable = StatusSkipped, true
			results = append(results, result)
			delete(scan.markedForRestart, candidate.Name)
			continue
		}
		err, seen := pulled[plan.newRef]
		if !seen {
			err = ErrImagePullerRequired
			if s.config.ImagePuller != nil {
				err = s.config.ImagePuller.PullImage(ctx, plan.newRef, io.Discard)
			}
			pulled[plan.newRef] = err
		}
		if err != nil {
			result.Status, result.Error = StatusFailed, fmt.Sprintf("pull target image: %v", err)
		} else {
			image, inspectErr := dockerClient.ImageInspect(ctx, plan.newRef)
			switch {
			case inspectErr != nil:
				result.Status, result.Error = StatusFailed, fmt.Sprintf("inspect pulled image: %v", inspectErr)
			case image.ID == "":
				result.Status, result.Error = StatusFailed, "pulled image has no image ID"
			case !opts.Force && image.ID == plan.inspect.Image && refs.NormalizeImageUpdateRef(plan.inspect.Config.Image) == plan.newRef:
				result.Status = StatusUpToDate
			default:
				continue
			}
		}
		results = append(results, result)
		delete(scan.markedForRestart, candidate.Name)
		plan.newRef = ""
	}
	return results
}
