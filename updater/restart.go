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
	return s.restartContainersUsingOldImages(ctx, oldIDToNewRef, oldRefToNewRef, enforceSettingsExclusions)
}

func (s *Service) restartContainersUsingOldImages(ctx context.Context, oldIDToNewRef, oldRefToNewRef map[string]string, exclusions exclusionPolicy) ([]ResourceResult, error) {
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
	proxy := dockerProxyContainerName(dockerClient.DaemonHost())

	updatedNorm := refs.NormalizeImageUpdateRefMapKeys(oldRefToNewRef)
	targetImageIDs := digestcheck.NewRefIDCache(digestcheck.NewChecker(dockerClient, nil))
	policy := s.config.LabelPolicy
	plansByName, markedForRestart := map[string]*restartPlan{}, map[string]bool{}
	var containers []deps.ContainerWithDeps
	for _, summary := range listResult.Items {
		optedOut := isExcluded(excluded, summary.ID, summary.Names...) || policy.IsUpdateDisabled(summary.Labels)
		name := containerSummaryName(summary)
		if (proxy != "" && isExcluded(map[string]bool{proxy: true}, summary.ID, summary.Names...)) || (optedOut && exclusions == enforceSettingsExclusions) ||
			policy.UnmanagedReason(summary.Labels) != "" ||
			(policy.IsSwarmTask(summary.Labels) && !policy.IsSelfUpdateTarget(summary.Labels)) {
			continue
		}

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
		if optedOut && newRef == "" {
			continue
		}
		containers = append(containers, deps.ContainerWithDeps{Container: summary, Name: name})
		plansByName[name] = &restartPlan{
			cnt:     summary,
			inspect: inspected,
			newRef:  newRef,
			match:   matchValue,
		}
		if newRef != "" {
			markedForRestart[name] = true
		}
	}

	if len(markedForRestart) == 0 {
		return nil, nil
	}
	for i, scanned := range containers {
		plan := plansByName[scanned.Name]
		if plan.inspect == nil {
			inspectResult, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, scanned.Container.ID, client.ContainerInspectOptions{})
			if inspectErr != nil {
				continue
			}
			plan.inspect = &inspectResult.Container
		}
		pinNetworkContainer(plan.inspect, listResult.Items)
		containers[i] = deps.ExtractContainerDeps(ctx, scanned.Name, scanned.Container, *plan.inspect)
	}
	scan := &restartScan{
		plansByName:      plansByName,
		markedForRestart: markedForRestart,
		containers:       containers,
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
			scan.plansByName[name] = &restartPlan{
				cnt:      plan.cnt,
				inspect:  plan.inspect,
				newRef:   cmp.Or(kit.FromPtr(kit.FromPtr(plan.inspect).Config).Image, plan.cnt.Image),
				match:    "dependency_restart",
				implicit: true,
			}
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
	slices.SortStableFunc(sorted, func(a, b deps.ContainerWithDeps) int {
		return cmp.Compare(s.restartRank(scan, a), s.restartRank(scan, b))
	})
	return sorted
}

// restartRank orders ordinary containers first, then agents, then the server hosting this process.
func (s *Service) restartRank(scan *restartScan, candidate deps.ContainerWithDeps) int {
	labels := candidate.Container.Labels
	if config := kit.FromPtr(kit.FromPtr(scan.plansByName[candidate.Name]).inspect).Config; config != nil {
		labels = config.Labels
	}
	return kit.Ternary(s.config.LabelPolicy.IsAgent(labels), 1, kit.Ternary(s.config.LabelPolicy.IsServer(labels), 2, 0))
}

// partitionRestartUnits splits scan containers into units joined by dependencies, Compose project, or a
// shared target ref, in discovery order. Units without a restart-marked member are dropped.
func partitionRestartUnits(scan *restartScan) [][]deps.ContainerWithDeps {
	parent := make(map[string]string, len(scan.containers))
	for _, candidate := range scan.containers {
		parent[candidate.Name] = candidate.Name
	}
	find := func(name string) string {
		for parent[name] != name {
			parent[name] = parent[parent[name]]
			name = parent[name]
		}
		return name
	}
	union := func(a, b string) {
		if _, known := parent[b]; known {
			parent[find(b)] = find(a)
		}
	}
	firstByKey := map[string]string{}
	for _, candidate := range scan.containers {
		for _, dep := range slices.Concat(candidate.Links, candidate.DependsOn, candidate.NetworkDeps) {
			union(candidate.Name, dep)
		}
		plan := kit.FromPtr(scan.plansByName[candidate.Name])
		project := cmp.Or(compose.ProjectLabel(labelsFromInspect(kit.FromPtr(plan.inspect))), compose.ProjectLabel(candidate.Container.Labels))
		for _, key := range []string{
			kit.Ternary(project != "", "project:"+project, ""),
			kit.Ternary(scan.markedForRestart[candidate.Name] && plan.newRef != "", "ref:"+plan.newRef, ""),
		} {
			if key == "" {
				continue
			}
			if first, seen := firstByKey[key]; seen {
				union(first, candidate.Name)
			} else {
				firstByKey[key] = candidate.Name
			}
		}
	}

	index := map[string]int{}
	var units [][]deps.ContainerWithDeps
	for _, candidate := range scan.containers {
		root := find(candidate.Name)
		if _, seen := index[root]; !seen {
			index[root] = len(units)
			units = append(units, nil)
		}
		units[index[root]] = append(units[index[root]], candidate)
	}
	return slices.DeleteFunc(units, func(unit []deps.ContainerWithDeps) bool {
		return !slices.ContainsFunc(unit, func(candidate deps.ContainerWithDeps) bool { return scan.markedForRestart[candidate.Name] })
	})
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
		if plan.newRef == "" {
			results = append(results, standaloneRestartResult(candidate.Name, plan, StatusSkipped, "no matching updated image"))
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
		res := standaloneRestartResult(candidate.Name, plan, StatusChecked, "")
		switch {
		case s.isSelfUpdateCandidate(plan.cnt.ID, labels):
			selfUpdateIndexes = append(selfUpdateIndexes, len(results))
		case isComposeTagChange(plan.inspect, plan.newRef) && projectID == "":
			res = standaloneRestartResult(candidate.Name, plan, StatusFailed, "compose tag update project could not be resolved")
		case projectID != "" && compose.ServiceLabel(labels) != "":
			res = s.applyComposeServiceUpdate(ctx, dockerClient, candidate.Name, plan, projectID, composeGroups[projectID], projectErrs)
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
		name := results[index].ResourceName
		plan := plansByName[name]
		endContainerStatus := s.BeginContainerUpdate(plan.cnt.ID)
		if err := s.triggerSelfUpdate(ctx, plan.cnt.ID, name, plan.newRef, labelsFromInspect(*plan.inspect)); err != nil {
			results[index] = standaloneRestartResult(name, plan, StatusFailed, err.Error())
		} else {
			results[index] = standaloneRestartResult(name, plan, StatusUpdated, "")
		}
		endContainerStatus()
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
	backups := make(map[string]string, len(candidates))
	for _, candidate := range slices.Backward(candidates) {
		plan := plansByName[candidate.Name]
		backupID, err := s.stopAndRenameStandaloneContainer(ctx, dockerClient, plan.cnt, *plan.inspect)
		if err != nil {
			results[candidate.Name] = standaloneRestartResult(candidate.Name, plan, StatusFailed, err.Error())
			continue
		}
		backups[candidate.Name] = backupID
		results[candidate.Name] = standaloneRestartResult(candidate.Name, plan, StatusChecked, "")
	}

	for _, candidate := range candidates {
		plan := plansByName[candidate.Name]
		if results[candidate.Name].Status == StatusFailed {
			continue
		}
		if err := s.createStartOrRollback(ctx, dockerClient, plan.cnt, *plan.inspect, plan.newRef, backups[candidate.Name]); err != nil {
			results[candidate.Name] = standaloneRestartResult(candidate.Name, plan, StatusFailed, err.Error())
			continue
		}
		if !plan.implicit {
			_ = s.notify(ctx, plan.cnt.ID, candidate.Name, plan.newRef, plan.match, refs.NormalizeImageUpdateRef(plan.newRef))
		}
		results[candidate.Name] = standaloneRestartResult(candidate.Name, plan, kit.Ternary(plan.implicit, StatusRestarted, StatusUpdated), "")
	}
	return results
}

// applyComposeServiceUpdate updates the candidate's Compose project once per run and verifies the service.
func (s *Service) applyComposeServiceUpdate(
	ctx context.Context,
	dockerClient *client.Client,
	name string,
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
	if group.tagChanges && projectErr != nil {
		return standaloneRestartResult(name, plan, StatusFailed, projectErr.Error())
	}
	verifyCtx, cancel := s.opCtx(ctx)
	verifyErr := verifyComposeService(verifyCtx, dockerClient, projectName, serviceName,
		kit.Ternary(group.tagChanges, "", match.CurrentContainerImageID(plan.cnt, plan.inspect)),
		kit.Ternary(group.tagChanges, plan.newRef, ""),
	)
	cancel()
	if verifyErr != nil {
		return standaloneRestartResult(name, plan, StatusFailed, kit.Ternary(projectErr != nil,
			fmt.Sprintf("project-level update failed: %v; service update verification failed: %v", projectErr, verifyErr),
			fmt.Sprintf("service update verification failed: %v", verifyErr),
		))
	}

	if projectErr != nil {
		s.logger.WarnContext(ctx, "service updated despite project-level compose error", "projectId", projectID, "projectName", projectName, "serviceName", serviceName, "error", projectErr)
	}
	_ = s.notify(ctx, plan.cnt.ID, name, plan.newRef, plan.match, refs.NormalizeImageUpdateRef(plan.newRef))
	return standaloneRestartResult(name, plan, kit.Ternary(plan.implicit, StatusRestarted, StatusUpdated), "")
}

func standaloneRestartResult(name string, plan *restartPlan, status ResourceStatus, message string) ResourceResult {
	return ResourceResult{
		ResourceID:      plan.cnt.ID,
		ResourceName:    name,
		ResourceType:    ResourceTypeContainer,
		Status:          status,
		UpdateAvailable: status == StatusUpdated,
		UpdateApplied:   status == StatusUpdated || status == StatusRestarted,
		OldImage:        plan.match,
		NewImage:        refs.NormalizeImageUpdateRef(plan.newRef),
		Error:           message,
	}
}
