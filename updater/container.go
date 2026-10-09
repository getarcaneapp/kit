package updater

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"go.getarcane.app/docker/compat"
	kit "go.getarcane.app/kit/pkg"

	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/internal/digestcheck"
	"go.getarcane.app/updater/refs"
	updatetypes "go.getarcane.app/updater/types"
)

// UpdateContainer updates a single Docker container by pulling its latest image and recreating it.
func (s *Service) UpdateContainer(ctx context.Context, containerID string, opts Options) (out *Result, err error) {
	out, finish := newTimedResult()
	defer finish(&err)

	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return nil, err
	}
	filters := make(client.Filters).Add("id", strings.TrimSpace(containerID))
	containerList, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	if len(containerList.Items) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrContainerNotFound, containerID)
	}

	target := containerList.Items[0]
	name := containerSummaryName(target)
	inspectResult, err := compat.ContainerInspectWithCompatibility(ctx, dockerClient, target.ID, client.ContainerInspectOptions{})
	if err != nil {
		appendResult(out, failedContainerResult(target.ID, name, fmt.Sprintf("inspect failed: %v", err)))
		return out, nil
	}
	inspect := inspectResult.Container
	labels := labelsFromInspect(inspect)
	defer s.BeginContainerUpdate(target.ID)()
	defer s.BeginProjectUpdate(compose.ProjectLabel(labels))()

	reason, err := s.containerEligibility(ctx, inspect, exclusionPolicy(!opts.IgnoreSettingsExclusions))
	if err != nil {
		return out, err
	}
	if reason == "" && (refs.IsDigestPinnedReference(inspect.Config.Image) || refs.IsImageIDLikeReference(inspect.Config.Image)) {
		reason = "immutable image reference"
	}
	if reason != "" {
		appendResult(out, skippedContainerResult(target.ID, name, reason))
		return out, nil
	}
	imageRef := refs.PullableImageRef(target.Image, inspect.Config.Image, nil)
	if imageRef == "" && inspect.Image != "" {
		if imageInspect, inspectErr := dockerClient.ImageInspect(ctx, inspect.Image); inspectErr == nil {
			imageRef = refs.PullableImageRef(target.Image, inspect.Config.Image, imageInspect.RepoTags)
		}
	}
	normalizedRef := refs.NormalizeImageUpdateRef(imageRef)
	if normalizedRef == "" {
		appendResult(out, skippedContainerResult(target.ID, name, "unable to resolve a pullable image reference for container"))
		return out, nil
	}

	selection, selectionErr := s.selectImageTag(ctx, normalizedRef, s.config.LabelPolicy.TagPolicy(labels))
	if selectionErr != nil {
		appendResult(out, failedContainerResult(target.ID, name, selectionErr.Error()))
		return out, nil
	}
	tagChanged := selection.UpdateAvailable && selection.UpdateType == string(UpdateTypeTag)
	normalizedRef = selection.TargetRef
	if _, preflightErr := s.preflightComposeImage(ctx, target.ID, inspect, normalizedRef); preflightErr != nil {
		appendResult(out, failedContainerResult(target.ID, name, preflightErr.Error()))
		return out, nil
	}
	if opts.DryRun {
		appendResult(out, ResourceResult{
			ResourceID: target.ID, ResourceName: name, ResourceType: ResourceTypeContainer, Status: StatusSkipped,
			UpdateAvailable: selection.UpdateAvailable, OldImage: imageRef, NewImage: normalizedRef,
		})
		return out, nil
	}

	if s.config.ImagePuller == nil {
		appendResult(out, failedContainerResult(target.ID, name, ErrImagePullerRequired.Error()))
		return out, nil
	}
	if pullErr := s.config.ImagePuller.PullImage(ctx, normalizedRef, io.Discard); pullErr != nil {
		appendResult(out, failedContainerResult(target.ID, name, fmt.Sprintf("pull failed: %v", pullErr)))
		return out, nil
	}

	changed, compareErr := digestcheck.NewChecker(dockerClient, nil).CompareWithPulled(ctx, inspect.Image, normalizedRef)
	if compareErr != nil && tagChanged {
		appendResult(out, failedContainerResult(target.ID, name, compareErr.Error()))
		return out, nil
	}
	if compareErr == nil && !changed && !opts.Force && !tagChanged {
		appendResult(out, skippedContainerResult(target.ID, name, "image digest unchanged after pull"))
		s.clearPendingRecord(ctx, target.ID, normalizedRef)
		return out, nil
	}

	selfUpdate := s.isSelfUpdateCandidate(target.ID, labels)
	var updateErr error
	if selfUpdate {
		updateErr = s.triggerSelfUpdate(ctx, target.ID, name, normalizedRef, labels)
	} else {
		updateErr = s.updateComposeOrStandalone(ctx, target, inspect, normalizedRef)
	}
	if updateErr != nil {
		appendResult(out, failedContainerResult(target.ID, name, updateErr.Error()))
		return out, nil
	}
	appendResult(out, ResourceResult{
		ResourceID: target.ID, ResourceName: name, ResourceType: ResourceTypeContainer, Status: StatusUpdated,
		UpdateAvailable: true, UpdateApplied: true, OldImage: inspect.Image, NewImage: normalizedRef,
	})
	if !selfUpdate {
		_ = s.notify(ctx, target.ID, name, imageRef, inspect.Image, normalizedRef)
	}
	s.clearPendingRecord(ctx, target.ID, normalizedRef)
	return out, nil
}

// clearPendingRecord clears the stored records an applied update satisfied, matching NewImageRef so a
// re-pulled old tag leaves its tag update pending. Records are cleared as stored, since stores key by ID.
func (s *Service) clearPendingRecord(ctx context.Context, containerID, imageRef string) {
	normalized := refs.NormalizeImageUpdateRef(imageRef)
	if s.config.PendingStore == nil || normalized == "" {
		return
	}
	records, err := s.config.PendingStore.PendingImageUpdates(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "failed to load pending records to clear applied update", "imageRef", imageRef, "error", err)
		return
	}
	for _, record := range records {
		if (record.ContainerID != "" && record.ContainerID != containerID) ||
			(record.IsTagUpdate() && record.ContainerID == "") ||
			refs.NormalizeImageUpdateRef(record.NewImageRef()) != normalized {
			continue
		}
		if err = s.config.PendingStore.ClearImageUpdateRecord(ctx, record); err != nil {
			s.logger.WarnContext(ctx, "failed to clear applied update record", "imageRef", imageRef, "error", err)
		}
	}
}

// updateComposeOrStandalone applies newRef through the container's Compose project when it resolves,
// and recreates the container standalone otherwise.
func (s *Service) updateComposeOrStandalone(ctx context.Context, target container.Summary, inspect container.InspectResponse, newRef string) error {
	labels := labelsFromInspect(inspect)
	if s.isSelfUpdateCandidate(target.ID, labels) {
		return ErrSelfUpdateContainer
	}
	projectName, serviceName := compose.ProjectLabel(labels), compose.ServiceLabel(labels)
	if isComposeTagChange(&inspect, newRef) {
		projectID, err := s.preflightComposeImage(ctx, target.ID, inspect, newRef)
		if err != nil {
			return err
		}
		adapter, _ := s.config.ProjectUpdater.(updatetypes.ProjectImageUpdater)
		opCtx, cancel := s.opCtx(ctx)
		err = adapter.UpdateServiceImages(opCtx, projectID, map[string]updatetypes.ServiceImageChange{
			serviceName: {ExpectedRef: inspect.Config.Image, TargetRef: newRef},
		})
		cancel()
		if err != nil {
			return fmt.Errorf("update compose service %s/%s image: %w", projectName, serviceName, err)
		}
		dockerClient, err := s.dockerClient(ctx)
		if err != nil {
			return err
		}
		return verifyComposeService(ctx, dockerClient, projectName, serviceName, "", newRef)
	}
	if projectName != "" && serviceName != "" && s.config.ProjectUpdater != nil {
		project, err := s.config.ProjectUpdater.ProjectByComposeName(ctx, projectName)
		if err == nil {
			// Never fall back once Compose owns the update, or a standalone recreate could clobber a partial up.
			opCtx, cancel := s.opCtx(ctx)
			defer cancel()
			return s.config.ProjectUpdater.UpdateServices(opCtx, project.ID, []string{serviceName})
		}
		s.logger.WarnContext(ctx, "compose project not resolved; falling back to standalone container update",
			"container", containerSummaryName(target),
			"project", projectName,
			"service", serviceName,
			"error", err,
		)
	}

	dockerClient, err := s.dockerClient(ctx)
	if err != nil {
		return err
	}
	if stopErr := s.stopAndRemoveStandaloneContainer(ctx, dockerClient, target, inspect); stopErr != nil {
		return stopErr
	}
	return s.createStartOrRollback(ctx, dockerClient, target, inspect, newRef)
}

// createStartOrRollback recreates the container as newRef, spelled like its configured image, and
// recreates it from its old image ID when that fails.
func (s *Service) createStartOrRollback(ctx context.Context, dockerClient *client.Client, cnt container.Summary, inspect container.InspectResponse, newRef string) error {
	if inspect.Config != nil {
		newRef = refs.PreserveConfiguredRef(inspect.Config.Image, newRef)
	}
	name := containerSummaryName(cnt)
	createdID, err := s.createAndStartStandaloneContainer(ctx, dockerClient, cnt, inspect, newRef)
	if err == nil {
		return nil
	}
	s.removeFailedCreatedContainer(ctx, dockerClient, createdID, name)

	rollbackID, rollbackErr := "", errors.New("old image ID unavailable")
	if strings.TrimSpace(inspect.Image) != "" {
		rollbackID, rollbackErr = s.createAndStartStandaloneContainer(ctx, dockerClient, cnt, inspect, inspect.Image)
	}
	if rollbackErr != nil {
		s.removeFailedCreatedContainer(ctx, dockerClient, rollbackID, name)
		return fmt.Errorf("%w; rollback failed: %w", err, rollbackErr)
	}
	_ = s.recordEvent(ctx, "container_rollback", rollbackID, name, map[string]any{
		"action":         "updater_rollback",
		"oldContainerID": cnt.ID,
		"rollbackImage":  inspect.Image,
	})
	return fmt.Errorf("%w; rollback succeeded", err)
}

func (s *Service) stopAndRemoveStandaloneContainer(ctx context.Context, dockerClient *client.Client, cnt container.Summary, inspect container.InspectResponse) error {
	name := containerSummaryName(cnt)
	stopCtx, cancelStop := s.opCtx(ctx)
	_, err := dockerClient.ContainerStop(stopCtx, cnt.ID, client.ContainerStopOptions{Signal: s.config.LabelPolicy.StopSignal(labelsFromInspect(inspect))})
	cancelStop()
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	_ = s.recordEvent(ctx, "container_stop", cnt.ID, name, map[string]any{"action": "updater_stop"})

	removeCtx, cancelRemove := s.opCtx(ctx)
	defer cancelRemove()
	if inspect.HostConfig != nil && inspect.HostConfig.AutoRemove {
		// The daemon removes --rm containers once stopped; wait so the name is free for the recreate.
		wait := dockerClient.ContainerWait(removeCtx, cnt.ID, client.ContainerWaitOptions{Condition: container.WaitConditionRemoved})
		select {
		case <-wait.Result:
		case err = <-wait.Error:
		}
	} else {
		_, err = dockerClient.ContainerRemove(removeCtx, cnt.ID, client.ContainerRemoveOptions{})
	}
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove: %w", err)
	}
	_ = s.recordEvent(ctx, "container_delete", cnt.ID, name, map[string]any{"action": "updater_delete"})
	return nil
}

// createAndStartStandaloneContainer recreates inspect's container from imageRef, starting it only if the old one ran.
func (s *Service) createAndStartStandaloneContainer(ctx context.Context, dockerClient *client.Client, cnt container.Summary, inspect container.InspectResponse, imageRef string) (string, error) {
	name := containerSummaryName(cnt)
	oldImage, err := dockerClient.ImageInspect(ctx, inspect.Image)
	if err != nil {
		s.logger.WarnContext(ctx, "could not inspect previous image; keeping its inherited container config", "image", inspect.Image, "error", err)
	}
	// Drop what the container inherited from its old image so the daemon merges in the new image's
	// defaults, as on a fresh create; an unknown old image strips nothing.
	imageConfig := cmp.Or(oldImage.Config, &dockerspec.DockerOCIImageConfig{})
	portBindings := kit.FromPtr(inspect.HostConfig).PortBindings
	cfg := new(kit.FromPtr(inspect.Config))
	cfg.Image = imageRef
	cfg.Hostname = kit.Ternary(cfg.Hostname == inspect.ID[:min(12, len(inspect.ID))], "", cfg.Hostname)
	cfg.Env = slices.DeleteFunc(slices.Clone(cfg.Env), func(env string) bool { return slices.Contains(imageConfig.Env, env) })
	cfg.Labels = maps.Clone(cfg.Labels)
	maps.DeleteFunc(cfg.Labels, func(key, value string) bool {
		imageValue, ok := imageConfig.Labels[key]
		return ok && imageValue == value
	})
	cfg.Volumes = maps.Clone(cfg.Volumes)
	maps.DeleteFunc(cfg.Volumes, func(target string, _ struct{}) bool {
		_, ok := imageConfig.Volumes[target]
		return ok
	})
	cfg.ExposedPorts = maps.Clone(cfg.ExposedPorts)
	maps.DeleteFunc(cfg.ExposedPorts, func(port network.Port, _ struct{}) bool {
		_, exposed := imageConfig.ExposedPorts[port.String()]
		_, bound := portBindings[port]
		return exposed && !bound
	})
	cfg.Entrypoint, cfg.Cmd = slices.Clone(cfg.Entrypoint), slices.Clone(cfg.Cmd)
	// The daemon only inherits the image's Cmd when Entrypoint is inherited too.
	if slices.Equal(cfg.Entrypoint, imageConfig.Entrypoint) {
		cfg.Entrypoint = nil
		if slices.Equal(cfg.Cmd, imageConfig.Cmd) {
			cfg.Cmd, cfg.ArgsEscaped = nil, false
		}
	}
	cfg.WorkingDir = kit.Ternary(cfg.WorkingDir == imageConfig.WorkingDir, "", cfg.WorkingDir)
	cfg.User = kit.Ternary(cfg.User == imageConfig.User, "", cfg.User)
	cfg.StopSignal = kit.Ternary(cfg.StopSignal == imageConfig.StopSignal, "", cfg.StopSignal)
	cfg.Shell = kit.Ternary(slices.Equal(cfg.Shell, imageConfig.Shell), nil, cfg.Shell)
	cfg.OnBuild = kit.Ternary(slices.Equal(cfg.OnBuild, imageConfig.OnBuild), nil, cfg.OnBuild)
	if cfg.Healthcheck != nil && imageConfig.Healthcheck != nil {
		health, imageHealth := *cfg.Healthcheck, imageConfig.Healthcheck
		health.Test = kit.Ternary(slices.Equal(health.Test, imageHealth.Test), nil, health.Test)
		health.Interval = kit.Ternary(health.Interval == imageHealth.Interval, 0, health.Interval)
		health.Timeout = kit.Ternary(health.Timeout == imageHealth.Timeout, 0, health.Timeout)
		health.StartPeriod = kit.Ternary(health.StartPeriod == imageHealth.StartPeriod, 0, health.StartPeriod)
		health.StartInterval = kit.Ternary(health.StartInterval == imageHealth.StartInterval, 0, health.StartInterval)
		health.Retries = kit.Ternary(health.Retries == imageHealth.Retries, 0, health.Retries)
		cfg.Healthcheck = &health
	}

	hostConfig, _, _, err := compat.PrepareRecreateHostConfigForEngine(ctx, dockerClient, inspect.HostConfig)
	if err != nil {
		return "", fmt.Errorf("prepare host config: %w", err)
	}
	var networkMode container.NetworkMode
	if hostConfig != nil {
		networkMode = hostConfig.NetworkMode
		pinVolumeMounts(hostConfig, inspect.Mounts)
	}
	if networkMode.IsHost() || networkMode.IsContainer() {
		cfg.Hostname, cfg.Domainname = "", ""
	}
	if networkMode.IsContainer() {
		cfg.ExposedPorts = nil
		hostConfig.PortBindings, hostConfig.PublishAllPorts = nil, false
	}

	apiVersion := compat.DetectDockerAPIVersion(ctx, dockerClient)
	var inspectOptions []client.ImageInspectOption
	if compat.IsDockerAPIVersionAtLeast(apiVersion, "1.48") {
		inspectOptions = append(inspectOptions, client.ImageInspectWithManifests(true))
	}
	newImage, err := dockerClient.ImageInspect(ctx, imageRef, inspectOptions...)
	if err != nil {
		s.logger.WarnContext(ctx, "could not inspect target image; keeping its Compose image label", "image", imageRef, "error", err)
	}
	if _, ok := cfg.Labels[compose.ImageLabelKey]; ok && err == nil {
		// Mirror Compose: the host platform's available image manifest, else the only available one, else the image ID.
		available := slices.DeleteFunc(slices.Clone(newImage.Manifests), func(m image.ManifestSummary) bool { return m.Kind != image.ManifestKindImage || !m.Available })
		matcher := platforms.Default()
		i := slices.IndexFunc(available, func(m image.ManifestSummary) bool { return m.ImageData != nil && matcher.Match(m.ImageData.Platform) })
		if i < 0 && len(available) == 1 {
			i = 0
		}
		cfg.Labels[compose.ImageLabelKey] = newImage.ID
		if i >= 0 {
			cfg.Labels[compose.ImageLabelKey] = available[i].ID
		}
	}

	var networkingConfig *network.NetworkingConfig
	if !networkMode.IsContainer() && inspect.NetworkSettings != nil {
		shortID := inspect.ID[:min(12, len(inspect.ID))]
		endpoints := make(map[string]*network.EndpointSettings, len(inspect.NetworkSettings.Networks))
		for networkName, endpoint := range inspect.NetworkSettings.Networks {
			endpoint = cmp.Or(endpoint, &network.EndpointSettings{})
			// Pre-1.44 inspect lists the old short ID as an alias; carrying it over would accumulate them.
			endpoints[networkName] = &network.EndpointSettings{
				IPAMConfig: endpoint.IPAMConfig,
				Links:      endpoint.Links,
				Aliases:    slices.DeleteFunc(slices.Clone(endpoint.Aliases), func(alias string) bool { return alias == shortID }),
				DriverOpts: endpoint.DriverOpts,
				GwPriority: endpoint.GwPriority,
				MacAddress: endpoint.MacAddress,
			}
		}
		if sanitized := compat.SanitizeContainerCreateEndpointSettingsForDockerAPI(endpoints, apiVersion); len(sanitized) > 0 {
			networkingConfig = &network.NetworkingConfig{EndpointsConfig: sanitized}
		}
	}

	createCtx, cancelCreate := s.opCtx(ctx)
	resp, err := compat.ContainerCreateWithCompatibilityForAPIVersion(createCtx, dockerClient, client.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       hostConfig,
		NetworkingConfig: networkingConfig,
		Name:             strings.TrimPrefix(inspect.Name, "/"),
	}, apiVersion)
	cancelCreate()
	if err != nil {
		return resp.ID, fmt.Errorf("create: %w", err)
	}
	_ = s.recordEvent(ctx, "container_create", resp.ID, name, map[string]any{"action": "updater_create", "newImageID": newImage.ID})

	if inspect.State != nil && (inspect.State.Running || inspect.State.Restarting) {
		startCtx, cancelStart := s.opCtx(ctx)
		_, err = dockerClient.ContainerStart(startCtx, resp.ID, client.ContainerStartOptions{})
		cancelStart()
		if err != nil {
			inspected, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, resp.ID, client.ContainerInspectOptions{})
			switch {
			case inspectErr != nil:
				return resp.ID, fmt.Errorf("start: %w; inspect after start error: %w", err, inspectErr)
			case inspected.Container.State == nil || !inspected.Container.State.Running:
				return resp.ID, fmt.Errorf("start: %w", err)
			}
			s.logger.WarnContext(ctx, "container start returned error but inspect reports running", "containerId", resp.ID, "containerName", name, "error", err)
		}
		_ = s.recordEvent(ctx, "container_start", resp.ID, name, map[string]any{"action": "updater_start"})
	}
	return resp.ID, s.recordEvent(ctx, "container_update", resp.ID, name, map[string]any{"oldContainerID": cnt.ID, "newContainerID": resp.ID, "newImage": imageRef})
}

// pinVolumeMounts reattaches every volume the old container mounted by name, so anonymous and
// image-declared volumes keep their data instead of being recreated empty.
func pinVolumeMounts(hostConfig *container.HostConfig, mountPoints []container.MountPoint) {
	hostConfig.Binds, hostConfig.Mounts = slices.Clone(hostConfig.Binds), slices.Clone(hostConfig.Mounts)
	for _, mountPoint := range mountPoints {
		if mountPoint.Type != mount.TypeVolume || mountPoint.Name == "" {
			continue
		}
		destination := path.Clean(mountPoint.Destination)
		if i := slices.IndexFunc(hostConfig.Mounts, func(m mount.Mount) bool { return path.Clean(m.Target) == destination }); i >= 0 {
			if hostConfig.Mounts[i].Type == mount.TypeVolume && hostConfig.Mounts[i].Source == "" {
				hostConfig.Mounts[i].Source = mountPoint.Name
			}
			continue
		}
		// Short syntax is "/dest" for an anonymous volume, else "source:/dest[:options]".
		i := slices.IndexFunc(hostConfig.Binds, func(bind string) bool {
			parts := strings.SplitN(bind, ":", 3)
			return path.Clean(parts[min(1, len(parts)-1)]) == destination
		})
		switch {
		case i < 0:
			hostConfig.Mounts = append(hostConfig.Mounts, mount.Mount{Type: mount.TypeVolume, Source: mountPoint.Name, Target: mountPoint.Destination, ReadOnly: !mountPoint.RW})
		case !strings.Contains(hostConfig.Binds[i], ":"):
			hostConfig.Binds[i] = mountPoint.Name + ":" + hostConfig.Binds[i]
		}
	}
}

func (s *Service) removeFailedCreatedContainer(ctx context.Context, dockerClient *client.Client, containerID, containerName string) {
	if containerID == "" {
		return
	}
	removeCtx, cancelRemove := s.opCtx(ctx)
	_, err := dockerClient.ContainerRemove(removeCtx, containerID, client.ContainerRemoveOptions{Force: true})
	cancelRemove()
	if err != nil {
		s.logger.WarnContext(ctx, "failed to remove container after unsuccessful recreate", "containerId", containerID, "containerName", containerName, "error", err)
		return
	}
	_ = s.recordEvent(ctx, "container_cleanup", containerID, containerName, map[string]any{"action": "updater_cleanup_failed_create"})
}

// pinNetworkContainer rewrites a container:<id> network mode to the owner's name, which survives its recreate.
func pinNetworkContainer(inspect *container.InspectResponse, containers []container.Summary) {
	if inspect.HostConfig == nil || !inspect.HostConfig.NetworkMode.IsContainer() {
		return
	}
	ref := inspect.HostConfig.NetworkMode.ConnectedContainer()
	if ref == "" || slices.ContainsFunc(containers, func(c container.Summary) bool { return containerSummaryName(c) == ref }) {
		return
	}
	if i := slices.IndexFunc(containers, func(c container.Summary) bool { return strings.HasPrefix(c.ID, ref) }); i >= 0 {
		inspect.HostConfig.NetworkMode = container.NetworkMode("container:" + containerSummaryName(containers[i]))
	}
}

// containerSummaryName returns the container's own name, skipping legacy link aliases such as
// "web/db", or its short ID when it has none.
func containerSummaryName(cnt container.Summary) string {
	name := ""
	if i := slices.IndexFunc(cnt.Names, func(n string) bool { return !strings.Contains(strings.TrimPrefix(n, "/"), "/") }); i >= 0 {
		name = strings.TrimPrefix(cnt.Names[i], "/")
	}
	return cmp.Or(name, cnt.ID[:min(12, len(cnt.ID))])
}
