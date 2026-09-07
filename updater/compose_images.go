package updater

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.getarcane.app/updater/internal/compat"
	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/refs"
	updatetypes "go.getarcane.app/updater/types"
)

func (s *Service) preflightComposeImageInternal(ctx context.Context, cnt container.Summary, inspect container.InspectResponse, newRef string) error {
	labels := labelsFromInspect(inspect)
	if s.isSelfUpdateCandidate(cnt.ID, labels) {
		return nil
	}
	if inspect.Config != nil && refs.NormalizeImageUpdateRef(inspect.Config.Image) == refs.NormalizeImageUpdateRef(newRef) {
		return nil
	}
	projectName, serviceName := compose.ProjectLabel(labels), compose.ServiceLabel(labels)
	if projectName == "" && serviceName == "" {
		projectName, serviceName = compose.ProjectLabel(cnt.Labels), compose.ServiceLabel(cnt.Labels)
	}
	if projectName == "" && serviceName == "" {
		return nil
	}
	if projectName == "" || serviceName == "" {
		return errors.New("compose tag update requires project and service labels")
	}
	if _, ok := s.config.ProjectUpdater.(updatetypes.ProjectImageUpdater); !ok {
		return fmt.Errorf("compose project %s requires a ProjectImageUpdater adapter for tag updates", projectName)
	}
	project, err := s.config.ProjectUpdater.ProjectByComposeName(ctx, projectName)
	if err != nil {
		return fmt.Errorf("resolve compose project %s for tag update: %w", projectName, err)
	}
	if strings.TrimSpace(project.ID) == "" {
		return fmt.Errorf("compose project %s has no project ID for tag update", projectName)
	}
	return nil
}

func (s *Service) updateComposeImageInternal(ctx context.Context, target container.Summary, inspect container.InspectResponse, newRef string) error {
	if err := s.preflightComposeImageInternal(ctx, target, inspect, newRef); err != nil {
		return err
	}
	labels := labelsFromInspect(inspect)
	projectName, serviceName := compose.ProjectLabel(labels), compose.ServiceLabel(labels)
	if projectName == "" && serviceName == "" {
		projectName, serviceName = compose.ProjectLabel(target.Labels), compose.ServiceLabel(target.Labels)
	}
	adapter, ok := s.config.ProjectUpdater.(updatetypes.ProjectImageUpdater)
	if !ok || projectName == "" || serviceName == "" || inspect.Config == nil {
		return errors.New("compose tag update requires an image updater and complete container configuration")
	}
	project, err := s.config.ProjectUpdater.ProjectByComposeName(ctx, projectName)
	if err != nil {
		return fmt.Errorf("resolve compose project %s for tag update: %w", projectName, err)
	}
	if strings.TrimSpace(project.ID) == "" {
		return fmt.Errorf("compose project %s has no project ID for tag update", projectName)
	}
	opCtx, cancel := s.opCtx(ctx)
	defer cancel()
	if err := adapter.UpdateServiceImages(opCtx, project.ID, map[string]updatetypes.ServiceImageChange{
		serviceName: {ExpectedRef: inspect.Config.Image, TargetRef: newRef},
	}); err != nil {
		return fmt.Errorf("update compose service %s/%s image: %w", projectName, serviceName, err)
	}
	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return err
	}
	return verifyComposeTargetInternal(ctx, dockerClient, projectName, serviceName, newRef)
}

func verifyComposeTargetInternal(ctx context.Context, dockerClient *client.Client, projectName, serviceName, newRef string) error {
	normalizedRef := refs.NormalizeImageUpdateRef(newRef)
	if dockerClient == nil || strings.TrimSpace(projectName) == "" || strings.TrimSpace(serviceName) == "" || normalizedRef == "" {
		return errors.New("verify compose target requires a Docker client, project, service, and valid target reference")
	}
	image, err := dockerClient.ImageInspect(ctx, newRef)
	if err != nil {
		return fmt.Errorf("verify compose target: inspect target image: %w", err)
	}
	if strings.TrimSpace(image.ID) == "" {
		return errors.New("verify compose target: target image has no image ID")
	}
	filters := make(client.Filters)
	filters = filters.Add("label", compose.ProjectLabelKey+"="+projectName)
	filters = filters.Add("label", compose.ServiceLabelKey+"="+serviceName)
	containers, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: false, Filters: filters})
	if err != nil {
		return fmt.Errorf("verify compose target: list service containers: %w", err)
	}
	if len(containers.Items) == 0 {
		return fmt.Errorf("compose service %s/%s has no running container after update", projectName, serviceName)
	}
	for _, cnt := range containers.Items {
		inspected, err := compat.ContainerInspect(ctx, dockerClient, cnt.ID, client.ContainerInspectOptions{})
		if err != nil {
			return fmt.Errorf("verify compose target: inspect container %s: %w", cnt.ID, err)
		}
		current := inspected.Container
		if current.Config == nil || refs.NormalizeImageUpdateRef(current.Config.Image) != normalizedRef || current.Image != image.ID || current.State == nil || !current.State.Running {
			return fmt.Errorf("compose service %s/%s container %s does not run target %s (%s)", projectName, serviceName, cnt.ID, newRef, image.ID)
		}
	}
	return nil
}
