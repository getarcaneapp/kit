package updater

import (
	"context"
	"fmt"

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
	if s.config.ProjectUpdater == nil {
		return groups
	}

	for _, candidate := range sorted {
		plan := plansByName[candidate.Name]
		if plan == nil || plan.newRef == "" || plan.inspect == nil || plan.inspect.Config == nil || s.config.LabelPolicy.IsSelfUpdateTarget(plan.inspect.Config.Labels) {
			continue
		}
		projectName, serviceName := compose.ProjectLabel(plan.inspect.Config.Labels), compose.ServiceLabel(plan.inspect.Config.Labels)
		if projectName == "" || serviceName == "" {
			continue
		}
		project, err := s.config.ProjectUpdater.ProjectByComposeName(ctx, projectName)
		if err != nil || project.ID == "" {
			continue
		}
		group := groups[project.ID]
		group.projectName = projectName
		if group.images == nil {
			group.images = map[string]types.ServiceImageChange{}
		}
		change := types.ServiceImageChange{ExpectedRef: plan.inspect.Config.Image, TargetRef: plan.newRef}
		previous, seen := group.images[serviceName]
		if seen && previous != change {
			group.err = fmt.Errorf("conflicting targets for compose service %s/%s", projectName, serviceName)
		}
		if !seen {
			group.services = append(group.services, serviceName)
		}
		group.images[serviceName] = change
		group.tagChanges = group.tagChanges || isComposeTagChange(plan.inspect, plan.newRef)
		groups[project.ID] = group
	}
	return groups
}
