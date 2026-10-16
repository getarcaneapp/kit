package updater

import (
	"context"
	"fmt"
	"slices"

	kit "go.getarcane.app/kit/pkg"

	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/internal/deps"
	"go.getarcane.app/updater/types"
)

type composeGroup struct {
	images      map[string]types.ServiceImageChange
	tagChanges  bool
	err         error
	projectName string
	services    []string
}

// buildComposeGroups groups planned Compose containers by resolved project, flagging conflicting service targets.
func (s *Service) buildComposeGroups(ctx context.Context, sorted []deps.ContainerWithDeps, plansByName map[string]*restartPlan) map[string]composeGroup {
	groups := map[string]composeGroup{}
	for _, candidate := range sorted {
		plan := kit.FromPtr(plansByName[candidate.Name])
		labels := kit.FromPtr(kit.FromPtr(plan.inspect).Config).Labels
		projectName, serviceName := compose.ProjectLabel(labels), compose.ServiceLabel(labels)
		if plan.newRef == "" || kit.FromPtr(plan.inspect).Config == nil || s.config.LabelPolicy.IsSelfUpdateTarget(labels) || projectName == "" || serviceName == "" {
			continue
		}
		project, err := s.config.ProjectUpdater.ProjectByComposeName(ctx, projectName)
		if err != nil || project.ID == "" {
			continue
		}
		group := groups[project.ID]
		change := types.ServiceImageChange{ExpectedRef: plan.inspect.Config.Image, TargetRef: plan.newRef}
		previous, seen := group.images[serviceName]
		images := kit.Ternary(group.images == nil, map[string]types.ServiceImageChange{}, group.images)
		images[serviceName] = change
		groups[project.ID] = composeGroup{
			images:      images,
			tagChanges:  group.tagChanges || isComposeTagChange(plan.inspect, plan.newRef),
			err:         kit.Ternary(seen && previous != change, fmt.Errorf("conflicting targets for compose service %s/%s", projectName, serviceName), group.err),
			projectName: projectName,
			services:    slices.Concat(group.services, kit.Ternary(seen, nil, []string{serviceName})),
		}
	}
	return groups
}
