package updater

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.getarcane.app/docker/compat"
	kit "go.getarcane.app/kit/pkg"

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
	plansByName, markedForRestart := map[string]*restartPlan{}, map[string]bool{}
	var containers []deps.ContainerWithDeps
	states := make([]targetedRecord, 0, len(records))
	for _, record := range records {
		if !record.NeedsUpdate() {
			continue
		}
		state, planned, collectErr := s.collectTargetRecord(ctx, dockerClient, listed.Items, proxy, record, plansByName, markedForRestart, out)
		if collectErr != nil {
			return collectErr
		}
		states = append(states, state)
		containers = append(containers, planned...)
	}
	if !opts.DryRun && len(markedForRestart) > 0 {
		containers = append(containers, s.collectImplicitCandidates(ctx, dockerClient, listed.Items, proxy, plansByName)...)
	}
	scan := &restartScan{
		plansByName:      plansByName,
		markedForRestart: markedForRestart,
		containers:       containers,
	}
	// All Compose conflicts and adapter capabilities must be checked before pulls.
	for _, group := range s.buildComposeGroups(ctx, s.sortRestartCandidates(ctx, scan), scan.plansByName) {
		if group.err != nil {
			return group.err
		}
	}

	// Each unit is pulled and recreated before the next is pulled, so a cancelled run never leaves
	// running containers on an image whose tag a pull already moved.
	units := partitionRestartUnits(scan)
	unitRank := func(unit []deps.ContainerWithDeps) int {
		rank := 0
		for _, candidate := range unit {
			rank = max(rank, s.restartRank(scan, candidate))
		}
		return rank
	}
	slices.SortStableFunc(units, func(a, b []deps.ContainerWithDeps) int { return cmp.Compare(unitRank(a), unitRank(b)) })
	// With per-operation bounds, a pulled unit finishes its recreate even after the run is cancelled.
	recreateCtx := kit.Ternary(s.config.OperationTimeout > 0, context.WithoutCancel(ctx), ctx)
	var results []ResourceResult
	for _, unit := range units {
		unitScan := &restartScan{
			plansByName:      scan.plansByName,
			markedForRestart: scan.markedForRestart,
			containers:       unit,
		}
		if ctx.Err() != nil {
			for _, candidate := range unit {
				if scan.markedForRestart[candidate.Name] {
					results = append(results, standaloneRestartResult(candidate.Name, scan.plansByName[candidate.Name], StatusFailed, fmt.Sprintf("update run cancelled before pull: %v", ctx.Err())))
				}
			}
			continue
		}
		results = append(results, s.pullTargetPlans(ctx, dockerClient, unitScan, opts)...)
		if opts.DryRun || !slices.ContainsFunc(unit, func(candidate deps.ContainerWithDeps) bool { return scan.markedForRestart[candidate.Name] }) {
			continue
		}
		propagateImplicitRestarts(unitScan)
		restarted, restartErr := s.executeRestartPlans(recreateCtx, dockerClient, s.sortRestartCandidates(ctx, unitScan), scan.plansByName)
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
		// Applied records are cleared even after cancellation; their containers already changed.
		if err = s.config.PendingStore.ClearImageUpdateRecord(context.WithoutCancel(ctx), state.record); err != nil {
			return fmt.Errorf("clear applied pending record: %w", err)
		}
	}
	return nil
}

// collectImplicitCandidates plans every other running, eligible container so dependents of targets can be restarted.
func (s *Service) collectImplicitCandidates(
	ctx context.Context,
	dockerClient *client.Client,
	listed []container.Summary,
	proxy string,
	plansByName map[string]*restartPlan,
) []deps.ContainerWithDeps {
	var implicit []deps.ContainerWithDeps
	for _, cnt := range listed {
		name := containerSummaryName(cnt)
		if _, planned := plansByName[name]; planned || cnt.State != container.StateRunning || name == proxy {
			continue
		}
		inspected, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			continue
		}
		if reason, eligibilityErr := s.containerEligibility(ctx, inspected.Container, enforceSettingsExclusions); eligibilityErr != nil || reason != "" {
			continue
		}
		pinNetworkContainer(&inspected.Container, listed)
		plansByName[name] = &restartPlan{cnt: cnt, inspect: &inspected.Container}
		implicit = append(implicit, deps.ExtractContainerDeps(ctx, name, cnt, inspected.Container))
	}
	return implicit
}

// collectTargetRecord plans every running container record targets and reports the ones it must reject.
func (s *Service) collectTargetRecord(
	ctx context.Context,
	dockerClient *client.Client,
	containers []container.Summary,
	proxy string,
	record ImageUpdateRecord,
	plansByName map[string]*restartPlan,
	markedForRestart map[string]bool,
	out *Result,
) (targetedRecord, []deps.ContainerWithDeps, error) {
	var targets []string
	var planned []deps.ContainerWithDeps
	failed := false
	for _, cnt := range containers {
		if cnt.State != container.StateRunning || (record.ContainerID != "" && record.ContainerID != cnt.ID) {
			continue
		}
		name := containerSummaryName(cnt)
		inspected, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			// An unscoped record cannot safely be cleared after an incomplete scan.
			failed = true
			s.appendRecordedResult(ctx, out, failedContainerResult(cnt.ID, name, inspectErr.Error()))
			continue
		}
		inspect := inspected.Container
		if record.ContainerID == "" && (inspect.Config == nil || refs.NormalizeImageUpdateRef(inspect.Config.Image) != refs.NormalizeImageUpdateRef(record.ImageRef())) {
			continue
		}
		if proxy != "" && name == proxy {
			failed = true
			s.appendRecordedResult(ctx, out, skippedContainerResult(cnt.ID, proxy, "Docker proxy excluded from pending updates"))
			continue
		}
		targets = append(targets, cnt.ID)
		plan, reason, planErr := s.targetRecordPlan(ctx, cnt, inspect, record)
		if planErr != nil || reason != "" {
			failed = true
			if reason != "" {
				s.appendRecordedResult(ctx, out, skippedContainerResult(cnt.ID, name, reason))
			} else {
				s.appendRecordedResult(ctx, out, failedContainerResult(cnt.ID, name, planErr.Error()))
			}
			continue
		}
		if previous := plansByName[name]; previous != nil {
			if previous.newRef != plan.newRef {
				return targetedRecord{}, nil, fmt.Errorf("conflicting image targets for container %s", cnt.ID)
			}
			continue
		}
		pinNetworkContainer(plan.inspect, containers)
		plansByName[name] = plan
		planned = append(planned, deps.ExtractContainerDeps(ctx, name, cnt, *plan.inspect))
		markedForRestart[name] = true
	}
	if len(targets) == 0 {
		s.appendRecordedResult(ctx, out, skippedContainerResult(record.ContainerID, record.ImageRef(), "pending update has no matching container"))
	}
	return targetedRecord{
		record:  record,
		targets: targets,
		failed:  failed || len(targets) == 0,
	}, planned, nil
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
	return &restartPlan{
		cnt:     cnt,
		inspect: &inspect,
		newRef:  newRef,
		match:   oldRef,
	}, "", nil
}

// pullTargetPlans pulls each planned target once, in scan order for deterministic outcomes, and unplans
// failed and no-op targets so they do not seed dependency restarts.
func (s *Service) pullTargetPlans(ctx context.Context, dockerClient *client.Client, scan *restartScan, opts Options) []ResourceResult {
	pulled := map[string]error{}
	var results []ResourceResult
	for _, candidate := range scan.containers {
		if !scan.markedForRestart[candidate.Name] {
			continue
		}
		plan := scan.plansByName[candidate.Name]
		if opts.DryRun {
			results = append(results, ResourceResult{
				ResourceID:      plan.cnt.ID,
				ResourceName:    candidate.Name,
				ResourceType:    ResourceTypeContainer,
				Status:          StatusSkipped,
				UpdateAvailable: true,
				OldImage:        plan.match,
				NewImage:        refs.NormalizeImageUpdateRef(plan.newRef),
			})
			delete(scan.markedForRestart, candidate.Name)
			continue
		}
		err, seen := pulled[plan.newRef]
		if !seen {
			err = s.config.ImagePuller.PullImage(ctx, plan.newRef, io.Discard)
			pulled[plan.newRef] = err
		}
		if err != nil {
			results = append(results, standaloneRestartResult(candidate.Name, plan, StatusFailed, fmt.Sprintf("pull target image: %v", err)))
		} else {
			image, inspectErr := dockerClient.ImageInspect(ctx, plan.newRef)
			switch {
			case inspectErr != nil:
				results = append(results, standaloneRestartResult(candidate.Name, plan, StatusFailed, fmt.Sprintf("inspect pulled image: %v", inspectErr)))
			case image.ID == "":
				results = append(results, standaloneRestartResult(candidate.Name, plan, StatusFailed, "pulled image has no image ID"))
			case !opts.Force && image.ID == plan.inspect.Image && refs.NormalizeImageUpdateRef(plan.inspect.Config.Image) == plan.newRef:
				results = append(results, standaloneRestartResult(candidate.Name, plan, StatusUpToDate, ""))
			default:
				continue
			}
		}
		delete(scan.markedForRestart, candidate.Name)
		plan.newRef = ""
	}
	return results
}
