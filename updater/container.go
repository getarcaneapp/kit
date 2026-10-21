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
	"time"

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

	selection, selectionErr := s.selectImageTag(ctx, "", normalizedRef, s.config.LabelPolicy.TagPolicy(labels))
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
			ResourceID:      target.ID,
			ResourceName:    name,
			ResourceType:    ResourceTypeContainer,
			Status:          StatusSkipped,
			UpdateAvailable: selection.UpdateAvailable,
			OldImage:        imageRef,
			NewImage:        normalizedRef,
		})
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
	updateErr := func() error {
		if selfUpdate {
			return s.triggerSelfUpdate(ctx, target.ID, name, normalizedRef, labels)
		}
		return s.updateComposeOrStandalone(ctx, target, inspect, normalizedRef)
	}()
	if updateErr != nil {
		appendResult(out, failedContainerResult(target.ID, name, updateErr.Error()))
		return out, nil
	}
	appendResult(out, ResourceResult{
		ResourceID:      target.ID,
		ResourceName:    name,
		ResourceType:    ResourceTypeContainer,
		Status:          StatusUpdated,
		UpdateAvailable: true,
		UpdateApplied:   true,
		OldImage:        inspect.Image,
		NewImage:        normalizedRef,
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
	if normalized == "" {
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
	if projectName != "" && serviceName != "" {
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
	backupID, stopErr := s.stopAndRenameStandaloneContainer(ctx, dockerClient, target, inspect)
	if stopErr != nil {
		return stopErr
	}
	return s.createStartOrRollback(ctx, dockerClient, target, inspect, newRef, backupID)
}

// createStartOrRollback recreates the container as newRef, spelled like its configured image, and
// restores backupID, or recreates it from its old image ID without one, when that fails.
func (s *Service) createStartOrRollback(ctx context.Context, dockerClient *client.Client, cnt container.Summary, inspect container.InspectResponse, newRef, backupID string) error {
	newRef = kit.Ternary(inspect.Config == nil, newRef, refs.PreserveConfiguredRef(kit.FromPtr(inspect.Config).Image, newRef))
	name := containerSummaryName(cnt)
	createdID, err := s.createAndStartStandaloneContainer(ctx, dockerClient, cnt, inspect, newRef)
	if err == nil {
		if backupID == "" {
			return nil
		}
		removeCtx, cancelRemove := s.opCtx(ctx)
		_, removeErr := dockerClient.ContainerRemove(removeCtx, backupID, client.ContainerRemoveOptions{Force: true})
		cancelRemove()
		if removeErr != nil && !cerrdefs.IsNotFound(removeErr) {
			s.logger.WarnContext(ctx, "failed to remove backup container after update", "containerId", backupID, "containerName", name, "error", removeErr)
			return nil
		}
		_ = s.recordEvent(ctx, "container_delete", backupID, name, map[string]any{"action": "updater_delete"})
		return nil
	}
	s.removeFailedCreatedContainer(ctx, dockerClient, createdID, name)

	if backupID != "" {
		restoreCtx, cancelRestore := s.opCtx(ctx)
		defer cancelRestore()
		if _, renameErr := dockerClient.ContainerRename(restoreCtx, backupID, client.ContainerRenameOptions{NewName: strings.TrimPrefix(inspect.Name, "/")}); renameErr != nil {
			return fmt.Errorf("%w; rollback failed: rename backup: %w", err, renameErr)
		}
		if kit.FromPtr(inspect.State).Running || kit.FromPtr(inspect.State).Restarting {
			if _, startErr := dockerClient.ContainerStart(restoreCtx, backupID, client.ContainerStartOptions{}); startErr != nil {
				return fmt.Errorf("%w; rollback failed: start backup: %w", err, startErr)
			}
		}
		_ = s.recordEvent(ctx, "container_rollback", backupID, name, map[string]any{
			"action":         "updater_rollback",
			"oldContainerID": cnt.ID,
			"rollbackImage":  inspect.Image,
		})
		return fmt.Errorf("%w; rollback succeeded", err)
	}

	if strings.TrimSpace(inspect.Image) == "" {
		return fmt.Errorf("%w; rollback failed: %w", err, errors.New("old image ID unavailable"))
	}
	rollbackID, rollbackErr := s.createAndStartStandaloneContainer(ctx, dockerClient, cnt, inspect, inspect.Image)
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

func (s *Service) stopAndRenameStandaloneContainer(ctx context.Context, dockerClient *client.Client, cnt container.Summary, inspect container.InspectResponse) (string, error) {
	name := containerSummaryName(cnt)
	stopCtx, cancelStop := s.opCtx(ctx)
	_, err := dockerClient.ContainerStop(stopCtx, cnt.ID, client.ContainerStopOptions{Signal: s.config.LabelPolicy.StopSignal(labelsFromInspect(inspect))})
	cancelStop()
	if err != nil {
		return "", fmt.Errorf("stop: %w", err)
	}
	_ = s.recordEvent(ctx, "container_stop", cnt.ID, name, map[string]any{"action": "updater_stop"})

	opCtx, cancelOp := s.opCtx(ctx)
	defer cancelOp()
	if kit.FromPtr(inspect.HostConfig).AutoRemove {
		// The daemon removes --rm containers once stopped; wait so the name is free for the recreate.
		wait := dockerClient.ContainerWait(opCtx, cnt.ID, client.ContainerWaitOptions{Condition: container.WaitConditionRemoved})
		select {
		case <-wait.Result:
		case err = <-wait.Error:
		}
		if err != nil && !cerrdefs.IsNotFound(err) {
			return "", fmt.Errorf("remove: %w", err)
		}
		_ = s.recordEvent(ctx, "container_delete", cnt.ID, name, map[string]any{"action": "updater_delete"})
		return "", nil
	}
	if _, err = dockerClient.ContainerRename(opCtx, cnt.ID, client.ContainerRenameOptions{
		NewName: fmt.Sprintf("%s-arcane-update-%d", strings.TrimPrefix(inspect.Name, "/"), time.Now().Unix()),
	}); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}
	return cnt.ID, nil
}

// createAndStartStandaloneContainer recreates inspect's container from imageRef, starting it only if the old one ran.
func (s *Service) createAndStartStandaloneContainer(ctx context.Context, dockerClient *client.Client, cnt container.Summary, inspect container.InspectResponse, imageRef string) (string, error) {
	name := containerSummaryName(cnt)
	oldImage, err := dockerClient.ImageInspect(ctx, inspect.Image)
	if err != nil {
		s.logger.WarnContext(ctx, "could not inspect previous image; keeping its inherited container config", "image", inspect.Image, "error", err)
	}
	hostConfig, _, _, err := compat.PrepareRecreateHostConfigForEngine(ctx, dockerClient, inspect.ID, inspect.HostConfig)
	if err != nil {
		return "", fmt.Errorf("prepare host config: %w", err)
	}
	networkMode := kit.FromPtr(hostConfig).NetworkMode
	if hostConfig != nil {
		// keep: hostConfig is the compat package's prepared copy; a full HostConfig literal reads worse
		hostConfig.Binds, hostConfig.Mounts = pinVolumeMounts(hostConfig.Binds, hostConfig.Mounts, inspect.Mounts)
	}
	if networkMode.IsContainer() {
		// keep: hostConfig is the compat package's prepared copy; a full HostConfig literal reads worse
		hostConfig.PortBindings, hostConfig.PublishAllPorts = nil, false
	}

	// Drop what the container inherited from its old image so the daemon merges in the new image's
	// defaults, as on a fresh create; an unknown old image strips nothing.
	imageConfig := cmp.Or(oldImage.Config, &dockerspec.DockerOCIImageConfig{})
	portBindings := kit.FromPtr(inspect.HostConfig).PortBindings
	// keep: by-value copy of the inspected config; a full 25-field container.Config literal reads worse
	cfg := new(kit.FromPtr(inspect.Config))
	cfg.Image = imageRef
	cfg.Hostname = kit.Ternary(cfg.Hostname == inspect.ID[:min(12, len(inspect.ID))] || networkMode.IsHost() || networkMode.IsContainer(), "", cfg.Hostname)
	cfg.Domainname = kit.Ternary(networkMode.IsHost() || networkMode.IsContainer(), "", cfg.Domainname)
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
	cfg.ExposedPorts = kit.Ternary(networkMode.IsContainer(), nil, maps.Clone(cfg.ExposedPorts))
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
		cfg.Healthcheck = new(container.HealthConfig{
			Test:          kit.Ternary(slices.Equal(cfg.Healthcheck.Test, imageConfig.Healthcheck.Test), nil, cfg.Healthcheck.Test),
			Interval:      kit.Ternary(cfg.Healthcheck.Interval == imageConfig.Healthcheck.Interval, 0, cfg.Healthcheck.Interval),
			Timeout:       kit.Ternary(cfg.Healthcheck.Timeout == imageConfig.Healthcheck.Timeout, 0, cfg.Healthcheck.Timeout),
			StartPeriod:   kit.Ternary(cfg.Healthcheck.StartPeriod == imageConfig.Healthcheck.StartPeriod, 0, cfg.Healthcheck.StartPeriod),
			StartInterval: kit.Ternary(cfg.Healthcheck.StartInterval == imageConfig.Healthcheck.StartInterval, 0, cfg.Healthcheck.StartInterval),
			Retries:       kit.Ternary(cfg.Healthcheck.Retries == imageConfig.Healthcheck.Retries, 0, cfg.Healthcheck.Retries),
		})
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

	networkingConfig := func() *network.NetworkingConfig {
		if networkMode.IsContainer() {
			return nil
		}
		shortID := inspect.ID[:min(12, len(inspect.ID))]
		networks := kit.FromPtr(inspect.NetworkSettings).Networks
		endpoints := make(map[string]*network.EndpointSettings, len(networks))
		for networkName, endpoint := range networks {
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
			return &network.NetworkingConfig{EndpointsConfig: sanitized}
		}
		return nil
	}()

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

	if kit.FromPtr(inspect.State).Running || kit.FromPtr(inspect.State).Restarting {
		startCtx, cancelStart := s.opCtx(ctx)
		_, err = dockerClient.ContainerStart(startCtx, resp.ID, client.ContainerStartOptions{})
		cancelStart()
		if err != nil {
			inspected, inspectErr := compat.ContainerInspectWithCompatibility(ctx, dockerClient, resp.ID, client.ContainerInspectOptions{})
			switch {
			case inspectErr != nil:
				return resp.ID, fmt.Errorf("start: %w; inspect after start error: %w", err, inspectErr)
			case !kit.FromPtr(inspected.Container.State).Running:
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
func pinVolumeMounts(binds []string, mounts []mount.Mount, mountPoints []container.MountPoint) ([]string, []mount.Mount) {
	binds, mounts = slices.Clone(binds), slices.Clone(mounts)
	for _, mountPoint := range mountPoints {
		if mountPoint.Type != mount.TypeVolume || mountPoint.Name == "" {
			continue
		}
		destination := path.Clean(mountPoint.Destination)
		if i := slices.IndexFunc(mounts, func(m mount.Mount) bool { return path.Clean(m.Target) == destination }); i >= 0 {
			if mounts[i].Type == mount.TypeVolume && mounts[i].Source == "" {
				// keep: fills the cloned mount's empty source; a mount.Mount literal would drop its options
				mounts[i].Source = mountPoint.Name
			}
			continue
		}
		// Short syntax is "/dest" for an anonymous volume, else "source:/dest[:options]".
		i := slices.IndexFunc(binds, func(bind string) bool {
			parts := strings.SplitN(bind, ":", 3)
			return path.Clean(parts[min(1, len(parts)-1)]) == destination
		})
		switch {
		case i < 0:
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeVolume,
				Source:   mountPoint.Name,
				Target:   mountPoint.Destination,
				ReadOnly: !mountPoint.RW,
			})
		case !strings.Contains(binds[i], ":"):
			binds[i] = mountPoint.Name + ":" + binds[i]
		}
	}
	return binds, mounts
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
	if !kit.FromPtr(inspect.HostConfig).NetworkMode.IsContainer() {
		return
	}
	ref := inspect.HostConfig.NetworkMode.ConnectedContainer()
	if ref == "" || slices.ContainsFunc(containers, func(c container.Summary) bool { return containerSummaryName(c) == ref }) {
		return
	}
	if i := slices.IndexFunc(containers, func(c container.Summary) bool { return strings.HasPrefix(c.ID, ref) }); i >= 0 {
		// keep: HostConfig is the inspected container's own; a full HostConfig literal reads worse
		inspect.HostConfig.NetworkMode = container.NetworkMode("container:" + containerSummaryName(containers[i]))
	}
}

// containerSummaryName returns the container's own name, skipping legacy link aliases such as
// "web/db", or its short ID when it has none.
func containerSummaryName(cnt container.Summary) string {
	if i := slices.IndexFunc(cnt.Names, func(n string) bool { return !strings.Contains(strings.TrimPrefix(n, "/"), "/") }); i >= 0 {
		return cmp.Or(strings.TrimPrefix(cnt.Names[i], "/"), cnt.ID[:min(12, len(cnt.ID))])
	}
	return cnt.ID[:min(12, len(cnt.ID))]
}
