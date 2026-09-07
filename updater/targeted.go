package updater

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.getarcane.app/updater/internal/compat"
	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/internal/deps"
	"go.getarcane.app/updater/pkg/utils/tagpolicy"
	"go.getarcane.app/updater/refs"
)

type targetedRecord struct {
	record  ImageUpdateRecord
	targets map[string]bool
	failed  bool
}

func (s *Service) applyTargetedRecordsInternal(ctx context.Context, records []ImageUpdateRecord, opts Options, out *Result) error {
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return err
	}
	listed, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return err
	}
	scan := &restartScan{plansByName: map[string]*restartPlan{}, markedForRestart: map[string]bool{}}
	states, err := s.collectTargetRecordsInternal(ctx, dockerClient, listed.Items, records, scan, out)
	if err != nil {
		return err
	}
	// All Compose conflicts and adapter capabilities must be checked before pulls.
	if err := s.validateComposeGroupsInternal(ctx, s.sortRestartCandidates(ctx, scan), scan.plansByName); err != nil {
		return err
	}
	results := s.pullTargetPlansInternal(ctx, dockerClient, scan, opts)
	if !opts.DryRun {
		s.addTargetDependenciesInternal(ctx, dockerClient, listed.Items, scan)
		sorted := s.sortRestartCandidates(ctx, scan)
		restarted, restartErr := s.executeRestartPlans(ctx, dockerClient, sorted, scan.plansByName)
		if restartErr != nil {
			return restartErr
		}
		results = append(results, restarted...)
	}
	successful := map[string]bool{}
	failed := map[string]bool{}
	for _, result := range results {
		s.appendTargetResultInternal(ctx, out, result)
		if result.Status == StatusFailed {
			failed[result.ResourceID] = true
		}
		successful[result.ResourceID] = !failed[result.ResourceID] && (successful[result.ResourceID] || result.Status == StatusUpdated || result.Status == StatusUpToDate)
	}
	if !opts.DryRun {
		return s.clearTargetedRecordsInternal(ctx, states, successful)
	}

	return nil
}

func (s *Service) targetRecordPlanInternal(ctx context.Context, cnt container.Summary, inspect container.InspectResponse, record ImageUpdateRecord) (*restartPlan, error) {
	reason, err := s.containerEligibilityInternal(ctx, inspect)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, errors.New(reason)
	}
	current := inspect.Config.Image
	if refs.IsDigestPinnedReference(current) || refs.IsImageIDLikeReference(current) {
		return nil, errors.New("immutable image reference")
	}
	oldRef, newRef := refs.NormalizeImageUpdateRef(record.ImageRef()), refs.NormalizeImageUpdateRef(record.NewImageRef())
	if oldRef == "" || newRef == "" {
		return nil, errors.New("invalid pending image reference")
	}
	current = refs.NormalizeImageUpdateRef(current)
	if current != oldRef && current != newRef {
		return nil, errors.New("container image changed since update check")
	}
	if record.IsTagUpdate() {
		old, parseErr := refs.NormalizeReference(oldRef)
		if parseErr != nil {
			return nil, parseErr
		}
		next, parseErr := refs.NormalizeReference(newRef)
		if parseErr != nil {
			return nil, parseErr
		}
		policy := s.config.LabelPolicy.TagPolicy(inspect.Config.Labels)
		if policy.Strategy != "" && policy.Strategy != "tag" {
			return nil, errors.New("tag updates disabled by current policy")
		}
		if record.ContainerID != "" && policy.Strategy != "tag" {
			return nil, errors.New("tag update policy changed since check")
		}
		policy.Strategy = "tag"
		selected, selectErr := tagpolicy.Select(old.Tag, []string{next.Tag}, policy)
		if selectErr != nil {
			return nil, selectErr
		}
		if selected != next.Tag || old.Tag == next.Tag {
			return nil, errors.New("pending tag is not a newer allowed version")
		}
	}
	if err := s.preflightComposeImageInternal(ctx, cnt, inspect, newRef); err != nil {
		return nil, err
	}
	return &restartPlan{cnt: cnt, inspect: &inspect, newRef: newRef, match: oldRef, explicit: true}, nil
}

func (s *Service) pullTargetPlansInternal(ctx context.Context, dockerClient *client.Client, scan *restartScan, opts Options) []ResourceResult {
	pulled := map[string]error{}
	var results []ResourceResult
	// Use scan order, rather than map order, for deterministic pulls and outcomes.
	for _, candidate := range scan.containers {
		plan := scan.plansByName[candidate.Name]
		result := standaloneRestartResult(candidate, plan)
		if opts.DryRun {
			result.Status = StatusSkipped
			result.UpdateAvailable = true
			results = append(results, result)
			delete(scan.markedForRestart, candidate.Name)
			continue
		}
		err, seen := pulled[plan.newRef]
		if !seen {
			if s.config.ImagePuller == nil {
				err = ErrImagePullerRequired
			} else {
				err = s.config.ImagePuller.PullImage(ctx, plan.newRef, io.Discard)
			}
			pulled[plan.newRef] = err
		}
		if err != nil {
			result.Status = StatusFailed
			result.Error = fmt.Sprintf("pull target image: %v", err)
		} else {
			image, inspectErr := dockerClient.ImageInspect(ctx, plan.newRef)
			switch {
			case inspectErr != nil:
				result.Status = StatusFailed
				result.Error = fmt.Sprintf("inspect pulled image: %v", inspectErr)
			case image.ID == "":
				result.Status = StatusFailed
				result.Error = "pulled image has no image ID"
			case !opts.Force && image.ID == plan.inspect.Image && refs.NormalizeImageUpdateRef(plan.inspect.Config.Image) == plan.newRef:
				result.Status = StatusUpToDate
			default:
				continue
			}
		}
		results = append(results, result)
		delete(scan.markedForRestart, candidate.Name)
		// Failed/no-op targets must not seed dependency propagation.
		plan.newRef = ""
	}
	return results
}

func (s *Service) addTargetDependenciesInternal(ctx context.Context, dockerClient *client.Client, containers []container.Summary, scan *restartScan) {
	if len(scan.markedForRestart) == 0 {
		return
	}
	for _, cnt := range containers {
		name := containerSummaryName(cnt)
		if _, ok := scan.plansByName[name]; ok {
			continue
		}
		if cnt.State != container.StateRunning || containerSummaryName(cnt) == dockerProxyContainerName(dockerHost(dockerClient)) {
			continue
		}
		inspected, err := compat.ContainerInspect(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
		if err != nil {
			continue
		}
		reason, err := s.containerEligibilityInternal(ctx, inspected.Container)
		if err != nil || reason != "" {
			continue
		}
		scan.plansByName[name] = &restartPlan{cnt: cnt, inspect: &inspected.Container}
		scan.containers = append(scan.containers, deps.ExtractContainerDeps(ctx, name, cnt, inspected.Container))
	}
	propagateImplicitRestarts(scan)
}

func (s *Service) appendTargetResultInternal(ctx context.Context, out *Result, result ResourceResult) {
	if result.Status != StatusUpToDate && result.Status != StatusChecked && result.Status != StatusUpdateAvailable {
		out.Checked++
	}
	s.applyResultCount(out, result)
	if result.Status == StatusUpToDate {
		out.Skipped++
	}
	out.Items = append(out.Items, result)
	_ = s.recordResult(ctx, result)
}

func (s *Service) validateComposeGroupsInternal(ctx context.Context, sorted []deps.ContainerWithDeps, plans map[string]*restartPlan) error {
	groups := s.buildComposeGroups(ctx, sorted, plans)
	for _, group := range groups {
		if group.err != nil {
			return group.err
		}
	}
	return nil
}

// isComposeTagChangeInternal includes implicit restarts in tag-aware groups but
// only requires image persistence when the configured image reference changes.
func isComposeTagChangeInternal(plan *restartPlan) bool {
	return plan.inspect != nil && plan.inspect.Config != nil && refs.NormalizeImageUpdateRef(plan.inspect.Config.Image) != refs.NormalizeImageUpdateRef(plan.newRef) && (compose.ProjectLabel(plan.inspect.Config.Labels) != "" || compose.ServiceLabel(plan.inspect.Config.Labels) != "")
}

func (s *Service) collectTargetRecordsInternal(ctx context.Context, dockerClient *client.Client, containers []container.Summary, records []ImageUpdateRecord, scan *restartScan, out *Result) ([]targetedRecord, error) {
	states := make([]targetedRecord, 0, len(records))
	for _, record := range records {
		if !record.NeedsUpdate() {
			continue
		}
		state, err := s.collectTargetRecordInternal(ctx, dockerClient, containers, record, scan, out)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}

	return states, nil
}

func (s *Service) collectTargetRecordInternal(ctx context.Context, dockerClient *client.Client, containers []container.Summary, record ImageUpdateRecord, scan *restartScan, out *Result) (targetedRecord, error) {
	state := targetedRecord{record: record, targets: map[string]bool{}}
	for _, cnt := range containers {
		if cnt.State != container.StateRunning {
			continue
		}
		if record.ContainerID != "" && record.ContainerID != cnt.ID {
			continue
		}
		if proxy := dockerProxyContainerName(dockerHost(dockerClient)); proxy != "" && containerSummaryName(cnt) == proxy {
			state.failed = true
			s.appendTargetResultInternal(ctx, out, skippedContainerResult(cnt.ID, proxy, "Docker proxy excluded from pending updates"))
			continue
		}
		inspected, inspectErr := compat.ContainerInspect(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			// An unscoped record cannot safely be cleared after an incomplete scan.
			state.failed = true
			s.appendTargetResultInternal(ctx, out, failedContainerResult(cnt.ID, containerSummaryName(cnt), inspectErr.Error()))
			continue
		}
		inspect := inspected.Container
		if record.ContainerID == "" && (inspect.Config == nil || refs.NormalizeImageUpdateRef(inspect.Config.Image) != refs.NormalizeImageUpdateRef(record.ImageRef())) {
			continue
		}
		state.targets[cnt.ID] = true
		plan, planErr := s.targetRecordPlanInternal(ctx, cnt, inspect, record)
		if planErr != nil {
			state.failed = true
			s.appendTargetResultInternal(ctx, out, failedContainerResult(cnt.ID, containerSummaryName(cnt), planErr.Error()))
			continue
		}
		name := containerSummaryName(cnt)
		if previous := scan.plansByName[name]; previous != nil {
			if previous.newRef != plan.newRef {
				return state, fmt.Errorf("conflicting image targets for container %s", cnt.ID)
			}
			continue
		}
		scan.plansByName[name] = plan
		scan.containers = append(scan.containers, deps.ExtractContainerDeps(ctx, name, cnt, inspect))
		scan.markedForRestart[name] = true
	}
	if len(state.targets) == 0 {
		state.failed = true
		s.appendTargetResultInternal(ctx, out, failedContainerResult(record.ContainerID, record.ImageRef(), "pending update has no matching container"))
	}

	return state, nil
}

func (s *Service) clearTargetedRecordsInternal(ctx context.Context, states []targetedRecord, successful map[string]bool) error {
	for _, state := range states {
		if state.failed {
			continue
		}
		all := true
		for id := range state.targets {
			if !successful[id] {
				all = false
			}
		}
		if all {
			if err := s.config.PendingStore.ClearImageUpdateRecord(ctx, state.record); err != nil {
				return fmt.Errorf("clear applied pending record: %w", err)
			}
		}
	}
	return nil
}
