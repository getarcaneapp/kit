package updater

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.getarcane.app/docker/compat"

	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/refs"
	updatetypes "go.getarcane.app/updater/types"
)

// preflightComposeImage resolves the Compose project a tag change to newRef must be persisted
// through, and returns "" when the change needs none.
func (s *Service) preflightComposeImage(ctx context.Context, containerID string, inspect container.InspectResponse, newRef string) (string, error) {
	labels := labelsFromInspect(inspect)
	if s.isSelfUpdateCandidate(containerID, labels) || !isComposeTagChange(&inspect, newRef) {
		return "", nil
	}
	projectName, serviceName := compose.ProjectLabel(labels), compose.ServiceLabel(labels)
	if projectName == "" || serviceName == "" {
		return "", errors.New("compose tag update requires project and service labels")
	}
	if _, ok := s.config.ProjectUpdater.(updatetypes.ProjectImageUpdater); !ok {
		return "", fmt.Errorf("compose project %s requires a ProjectImageUpdater adapter for tag updates", projectName)
	}
	project, err := s.config.ProjectUpdater.ProjectByComposeName(ctx, projectName)
	if err != nil {
		return "", fmt.Errorf("resolve compose project %s for tag update: %w", projectName, err)
	}
	if strings.TrimSpace(project.ID) == "" {
		return "", fmt.Errorf("compose project %s has no project ID for tag update", projectName)
	}
	return project.ID, nil
}

// isComposeTagChange reports whether recreating a Compose container as newRef changes its configured image,
// which only a ProjectImageUpdater can persist.
func isComposeTagChange(inspect *container.InspectResponse, newRef string) bool {
	return inspect != nil && inspect.Config != nil &&
		refs.NormalizeImageUpdateRef(inspect.Config.Image) != refs.NormalizeImageUpdateRef(newRef) &&
		(compose.ProjectLabel(inspect.Config.Labels) != "" || compose.ServiceLabel(inspect.Config.Labels) != "")
}

// verifyComposeService checks that a service's running containers run targetRef or, without a target,
// no longer run oldImageID.
func verifyComposeService(ctx context.Context, dockerClient *client.Client, projectName, serviceName, oldImageID, targetRef string) error {
	if targetRef == "" && oldImageID == "" {
		return nil
	}
	targetID := ""
	if targetRef != "" {
		image, err := dockerClient.ImageInspect(ctx, targetRef)
		if err != nil {
			return fmt.Errorf("verify compose service: inspect target image: %w", err)
		}
		if targetID = strings.TrimSpace(image.ID); targetID == "" {
			return errors.New("verify compose service: target image has no image ID")
		}
	}
	filters := make(client.Filters).Add("label", compose.ProjectLabelKey+"="+projectName, compose.ServiceLabelKey+"="+serviceName, compose.ServiceContainerFilter)
	containers, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{Filters: filters})
	if err != nil {
		return fmt.Errorf("verify compose service: list service containers: %w", err)
	}
	if len(containers.Items) == 0 {
		return fmt.Errorf("compose service %s/%s has no running container after update", projectName, serviceName)
	}
	for _, cnt := range containers.Items {
		currentImageID := strings.TrimSpace(cnt.ImageID)
		var current container.InspectResponse
		if targetID != "" || currentImageID == "" {
			inspected, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
			if inspectErr != nil {
				return fmt.Errorf("verify compose service: inspect container %s: %w", cnt.ID, inspectErr)
			}
			current = inspected.Container
			currentImageID = strings.TrimSpace(current.Image)
		}
		switch {
		case targetID == "" && currentImageID == oldImageID:
			return fmt.Errorf("compose service %s/%s still running old image %s after update", projectName, serviceName, oldImageID)
		case targetID != "" && (current.Config == nil || refs.NormalizeImageUpdateRef(current.Config.Image) != refs.NormalizeImageUpdateRef(targetRef) ||
			currentImageID != targetID || current.State == nil || !current.State.Running):
			return fmt.Errorf("compose service %s/%s container %s does not run target %s (%s)", projectName, serviceName, cnt.ID, targetRef, targetID)
		}
	}
	return nil
}
