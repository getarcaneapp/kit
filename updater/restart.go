package updater

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.getarcane.app/docker/compat"
	kit "go.getarcane.app/kit/pkg"

	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/internal/deps"
	"go.getarcane.app/updater/internal/digestcheck"
	"go.getarcane.app/updater/internal/match"
	"go.getarcane.app/updater/refs"
	"go.getarcane.app/updater/types"
)

// restartScan holds the per-container plans, the restart-marked set, and every eligible container with its dependencies.
type restartScan struct {
	plansByName      map[string]*restartPlan
	markedForRestart map[string]bool
	containers       []deps.ContainerWithDeps
}

// RestartContainersUsingOldImages restarts running containers matching old image IDs or refs, plus
// their dependents. A dependency cycle falls back to discovery order.
func (s *Service) RestartContainersUsingOldImages(ctx context.Context, oldIDToNewRef, oldRefToNewRef map[string]string) ([]ResourceResult, error) {
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return nil, err
	}
	listResult, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: false})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	excluded, err := s.excludedContainerSet(ctx)
	if err != nil {
		return nil, err
	}
	if proxy := dockerProxyContainerName(dockerClient.DaemonHost()); proxy != "" {
		excluded[proxy] = true
	}

	updatedNorm := refs.NormalizeImageUpdateRefMapKeys(oldRefToNewRef)
	targetImageIDs := digestcheck.NewRefIDCache(digestcheck.NewChecker(dockerClient, nil))
	policy := s.config.LabelPolicy
	scan := &restartScan{plansByName: map[string]*restartPlan{}, markedForRestart: map[string]bool{}}
	for _, summary := range listResult.Items {
		if isExcluded(excluded, summary.ID, summary.Names...) || policy.IsUpdateDisabled(summary.Labels) ||
			(policy.IsSwarmTask(summary.Labels) && !policy.IsSelfUpdateTarget(summary.Labels)) {
			continue
		}
		name := containerSummaryName(summary)
		scan.containers = append(scan.containers, deps.ContainerWithDeps{Container: summary, Name: name})

		// Inspect only when the summary alone cannot settle the match.
		var inspected *container.InspectResponse
		newRef, matchValue := match.ResolveContainerImageMatch(summary, nil, oldIDToNewRef, updatedNorm)
		if newRef == "" && match.ShouldInspectUnmatchedContainerForImageMatch(summary) {
			if inspectResult, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, summary.ID, client.ContainerInspectOptions{}); inspectErr == nil {
				inspected = &inspectResult.Container
				newRef, matchValue = match.ResolveContainerImageMatch(summary, inspected, oldIDToNewRef, updatedNorm)
			}
		}
		currentImageID := match.CurrentContainerImageID(summary, inspected)
		if newRef != "" && currentImageID != "" && slices.Contains(targetImageIDs.IDsForRef(ctx, newRef), currentImageID) {
			newRef = ""
		}
		scan.plansByName[name] = &restartPlan{cnt: summary, inspect: inspected, newRef: newRef, match: matchValue}
		if newRef != "" {
			scan.markedForRestart[name] = true
		}
	}

	if len(scan.markedForRestart) == 0 {
		return nil, nil
	}
	for i, scanned := range scan.containers {
		plan := scan.plansByName[scanned.Name]
		if plan.inspect == nil {
			inspectResult, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, scanned.Container.ID, client.ContainerInspectOptions{})
			if inspectErr != nil {
				continue
			}
			plan.inspect = &inspectResult.Container
		}
		pinNetworkContainer(plan.inspect, listResult.Items)
		scan.containers[i] = deps.ExtractContainerDeps(ctx, scanned.Name, scanned.Container, *plan.inspect)
	}
	propagateImplicitRestarts(scan)
	return s.executeRestartPlans(ctx, dockerClient, s.sortRestartCandidates(ctx, scan), scan.plansByName)
}

// propagateImplicitRestarts marks dependents of restarting containers until the set stops growing.
func propagateImplicitRestarts(scan *restartScan) {
	for {
		added := deps.UpdateImplicitRestart(scan.containers, scan.markedForRestart)
		if len(added) == 0 {
			return
		}
		for _, name := range added {
			plan, ok := scan.plansByName[name]
			if !ok || plan.newRef != "" {
				continue
			}
			configImage := ""
			if plan.inspect != nil && plan.inspect.Config != nil {
				configImage = plan.inspect.Config.Image
			}
			plan.newRef, plan.match, plan.implicit = cmp.Or(configImage, plan.cnt.Image), "dependency_restart", true
		}
	}
}

// sortRestartCandidates orders restart-marked containers by dependency (discovery order on cycles), then
// moves self-update targets last with agents before the server hosting this process.
func (s *Service) sortRestartCandidates(ctx context.Context, scan *restartScan) []deps.ContainerWithDeps {
	candidates := slices.DeleteFunc(slices.Clone(scan.containers), func(cd deps.ContainerWithDeps) bool { return !scan.markedForRestart[cd.Name] })
	sorted, sortErr := deps.NewContainerSorter(candidates).Sort()
	if sortErr != nil {
		s.logger.WarnContext(ctx, "container dependency sort failed; restarting in discovery order", "error", sortErr)
		sorted = candidates
	}
	rank := func(candidate deps.ContainerWithDeps) int {
		labels := candidate.Container.Labels
		if plan := scan.plansByName[candidate.Name]; plan != nil && plan.inspect != nil && plan.inspect.Config != nil {
			labels = plan.inspect.Config.Labels
		}
		return kit.Ternary(s.config.LabelPolicy.IsAgent(labels), 1, kit.Ternary(s.config.LabelPolicy.IsServer(labels), 2, 0))
	}
	slices.SortStableFunc(sorted, func(a, b deps.ContainerWithDeps) int { return cmp.Compare(rank(a), rank(b)) })
	return sorted
}

// executeRestartPlans routes each sorted candidate through Compose, the standalone recreate, or, last
// of all because it may stop this process, the self-updater.
func (s *Service) executeRestartPlans(ctx context.Context, dockerClient *client.Client, sorted []deps.ContainerWithDeps, plansByName map[string]*restartPlan) ([]ResourceResult, error) {
	composeGroups := s.buildComposeGroups(ctx, sorted, plansByName)
	projectErrs := map[string]error{}
	var results []ResourceResult
	var standalone []deps.ContainerWithDeps
	standaloneIndexes := map[string]int{}
	var selfUpdateIndexes []int

	for _, candidate := range sorted {
		plan := plansByName[candidate.Name]
		if plan == nil {
			continue
		}
		if plan.inspect == nil {
			inspectResult, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, plan.cnt.ID, client.ContainerInspectOptions{})
			if inspectErr != nil {
				results = append(results, failedContainerResult(plan.cnt.ID, candidate.Name, fmt.Sprintf("inspect failed: %v", inspectErr)))
				continue
			}
			plan.inspect = new(inspectResult.Container)
		}
		res := standaloneRestartResult(candidate, plan)
		if plan.newRef == "" {
			res.Status, res.Error = StatusSkipped, "no matching updated image"
			results = append(results, res)
			continue
		}

		labels := labelsFromInspect(*plan.inspect)
		projectName := compose.ProjectLabel(labels)
		endContainerStatus := s.BeginContainerUpdate(plan.cnt.ID)
		endProjectStatus := s.BeginProjectUpdate(projectName)
		projectID := ""
		for id, group := range composeGroups {
			if projectName != "" && group.projectName == projectName {
				projectID = id
				break
			}
		}
		selfUpdate := s.isSelfUpdateCandidate(plan.cnt.ID, labels)
		switch {
		case selfUpdate:
			selfUpdateIndexes = append(selfUpdateIndexes, len(results))
		case isComposeTagChange(plan.inspect, plan.newRef) && projectID == "":
			res.Status, res.Error = StatusFailed, "compose tag update project could not be resolved"
		case projectID != "" && compose.ServiceLabel(labels) != "":
			res = s.applyComposeServiceUpdate(ctx, dockerClient, res, plan, projectID, composeGroups[projectID], projectErrs)
		default:
			standaloneIndexes[candidate.Name] = len(results)
			standalone = append(standalone, candidate)
		}
		endProjectStatus()
		endContainerStatus()
		results = append(results, res)
	}

	if len(standalone) > 0 {
		for name, result := range s.updateStandaloneRestartCandidates(ctx, dockerClient, standalone, plansByName) {
			results[standaloneIndexes[name]] = result
		}
	}

	// Self-updates arrive sorted agents first, so the server hosting this process goes last.
	for _, index := range selfUpdateIndexes {
		res := results[index]
		plan := plansByName[res.ResourceName]
		endContainerStatus := s.BeginContainerUpdate(plan.cnt.ID)
		if err := s.triggerSelfUpdate(ctx, plan.cnt.ID, res.ResourceName, plan.newRef, labelsFromInspect(*plan.inspect)); err != nil {
			res.Status, res.Error = StatusFailed, err.Error()
		} else {
			res.Status, res.UpdateAvailable, res.UpdateApplied = StatusUpdated, true, true
		}
		endContainerStatus()
		results[index] = res
	}
	return results, nil
}

// updateStandaloneRestartCandidates stops dependents before what they depend on, then recreates in dependency order.
func (s *Service) updateStandaloneRestartCandidates(
	ctx context.Context,
	dockerClient *client.Client,
	candidates []deps.ContainerWithDeps,
	plansByName map[string]*restartPlan,
) map[string]ResourceResult {
	endStatus := make([]func(), 0, len(candidates))
	for _, candidate := range candidates {
		endStatus = append(endStatus, s.BeginContainerUpdate(plansByName[candidate.Name].cnt.ID))
	}
	defer func() {
		for _, end := range slices.Backward(endStatus) {
			end()
		}
	}()

	results := make(map[string]ResourceResult, len(candidates))
	for _, candidate := range slices.Backward(candidates) {
		plan := plansByName[candidate.Name]
		result := standaloneRestartResult(candidate, plan)
		if err := s.stopAndRemoveStandaloneContainer(ctx, dockerClient, plan.cnt, *plan.inspect); err != nil {
			result.Status, result.Error = StatusFailed, err.Error()
		}
		results[candidate.Name] = result
	}

	for _, candidate := range candidates {
		plan, result := plansByName[candidate.Name], results[candidate.Name]
		if result.Status == StatusFailed {
			continue
		}
		if err := s.createStartOrRollback(ctx, dockerClient, plan.cnt, *plan.inspect, plan.newRef); err != nil {
			result.Status, result.Error = StatusFailed, err.Error()
			results[candidate.Name] = result
			continue
		}
		result.Status = kit.Ternary(plan.implicit, StatusRestarted, StatusUpdated)
		result.UpdateAvailable, result.UpdateApplied = !plan.implicit, true
		if !plan.implicit {
			_ = s.notify(ctx, plan.cnt.ID, candidate.Name, plan.newRef, plan.match, refs.NormalizeImageUpdateRef(plan.newRef))
		}
		results[candidate.Name] = result
	}
	return results
}

// applyComposeServiceUpdate updates the candidate's Compose project once per run and verifies the service.
func (s *Service) applyComposeServiceUpdate(
	ctx context.Context,
	dockerClient *client.Client,
	res ResourceResult,
	plan *restartPlan,
	projectID string,
	group composeGroup,
	projectErrs map[string]error,
) ResourceResult {
	projectErr, processed := projectErrs[projectID]
	if !processed {
		opCtx, cancel := s.opCtx(ctx)
		adapter, ok := s.config.ProjectUpdater.(types.ProjectImageUpdater)
		switch {
		case group.err != nil:
			projectErr = group.err
		case group.tagChanges && !ok:
			projectErr = errors.New("compose tag updates require a ProjectImageUpdater adapter")
		case group.tagChanges:
			projectErr = adapter.UpdateServiceImages(opCtx, projectID, group.images)
		default:
			projectErr = s.config.ProjectUpdater.UpdateServices(opCtx, projectID, group.services)
		}
		cancel()
		projectErrs[projectID] = projectErr
	}

	labels := labelsFromInspect(*plan.inspect)
	projectName, serviceName := compose.ProjectLabel(labels), compose.ServiceLabel(labels)
	oldImageID, targetRef := match.CurrentContainerImageID(plan.cnt, plan.inspect), ""
	if group.tagChanges {
		if projectErr != nil {
			res.Status, res.Error = StatusFailed, projectErr.Error()
			return res
		}
		oldImageID, targetRef = "", plan.newRef
	}
	if verifyErr := verifyComposeService(ctx, dockerClient, projectName, serviceName, oldImageID, targetRef); verifyErr != nil {
		res.Status = StatusFailed
		res.Error = fmt.Sprintf("service update verification failed: %v", verifyErr)
		if projectErr != nil {
			res.Error = fmt.Sprintf("project-level update failed: %v; service update verification failed: %v", projectErr, verifyErr)
		}
		return res
	}

	if projectErr != nil {
		s.logger.WarnContext(ctx, "service updated despite project-level compose error", "projectId", projectID, "projectName", projectName, "serviceName", serviceName, "error", projectErr)
	}
	res.Status = kit.Ternary(plan.implicit, StatusRestarted, StatusUpdated)
	res.UpdateAvailable, res.UpdateApplied = !plan.implicit, true
	_ = s.notify(ctx, plan.cnt.ID, res.ResourceName, plan.newRef, plan.match, refs.NormalizeImageUpdateRef(plan.newRef))
	return res
}

func standaloneRestartResult(candidate deps.ContainerWithDeps, plan *restartPlan) ResourceResult {
	return ResourceResult{
		ResourceID:   plan.cnt.ID,
		ResourceName: candidate.Name,
		ResourceType: ResourceTypeContainer,
		Status:       StatusChecked,
		OldImage:     plan.match,
		NewImage:     refs.NormalizeImageUpdateRef(plan.newRef),
	}
}
