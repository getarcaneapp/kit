package updater

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/moby/moby/client"
	kit "go.getarcane.app/kit/pkg"

	"go.getarcane.app/updater/internal/compose"
)

type dockerComposeProjectUpdater struct {
	dockerClientProvider DockerClientProvider
}

type dockerComposeProjectMetadata struct {
	projectName string
	workingDir  string
	configFiles []string
	envFiles    []string
	dockerHost  string
}

// NewDockerComposeProjectUpdater returns a Docker Compose CLI project updater.
func NewDockerComposeProjectUpdater(provider DockerClientProvider) ProjectUpdater {
	return dockerComposeProjectUpdater{dockerClientProvider: cmp.Or[DockerClientProvider](provider, NewDockerClientProvider())}
}

func (u dockerComposeProjectUpdater) ProjectByComposeName(ctx context.Context, composeName string) (ComposeProject, error) {
	metadata, err := u.resolveProjectMetadata(ctx, composeName)
	if err != nil {
		return ComposeProject{}, err
	}
	return ComposeProject{ID: metadata.projectName, Name: metadata.projectName}, nil
}

// UpdateServices recreates services with the project directory, config and env files Compose recorded,
// against the same engine the provider uses.
func (u dockerComposeProjectUpdater) UpdateServices(ctx context.Context, projectID string, services []string) error {
	metadata, err := u.resolveProjectMetadata(ctx, projectID)
	if err != nil {
		return err
	}
	services = kit.Unique(kit.TrimNonEmpty(services))
	if len(services) == 0 {
		return errors.New("compose update requires at least one service")
	}

	args := []string{"compose", "-p", metadata.projectName}
	if metadata.workingDir != "" {
		args = append(args, "--project-directory", metadata.workingDir)
	}
	for _, configFile := range metadata.configFiles {
		args = append(args, "-f", configFile)
	}
	for _, envFile := range metadata.envFiles {
		args = append(args, "--env-file", envFile)
	}
	args = append(args, "up", "-d", "--no-deps", "--force-recreate")
	args = append(args, services...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = metadata.workingDir
	if metadata.dockerHost != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+metadata.dockerHost)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		const maxComposeErrorOutput = 4096
		message := strings.TrimSpace(string(output))
		if len(message) > maxComposeErrorOutput {
			message = message[:maxComposeErrorOutput] + " (truncated)"
		}
		return fmt.Errorf("docker compose update failed: %w: %s", err, message)
	}
	return nil
}

// resolveProjectMetadata reads a project's Compose labels from one of its service containers.
func (u dockerComposeProjectUpdater) resolveProjectMetadata(ctx context.Context, composeName string) (dockerComposeProjectMetadata, error) {
	composeName = strings.TrimSpace(composeName)
	if composeName == "" {
		return dockerComposeProjectMetadata{}, errors.New("compose project name is required")
	}

	dockerClient, err := u.dockerClientProvider.DockerClient(ctx)
	if err != nil {
		return dockerComposeProjectMetadata{}, fmt.Errorf("docker connect: %w", err)
	}

	filters := make(client.Filters).Add("label", compose.ProjectLabelKey+"="+composeName, compose.ServiceContainerFilter)
	containers, err := dockerClient.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return dockerComposeProjectMetadata{}, fmt.Errorf("list compose containers: %w", err)
	}
	if len(containers.Items) == 0 {
		return dockerComposeProjectMetadata{}, fmt.Errorf("compose project not found: %s", composeName)
	}
	labels := containers.Items[0].Labels
	return dockerComposeProjectMetadata{
		projectName: composeName,
		workingDir:  strings.TrimSpace(labels[compose.WorkingDirLabelKey]),
		configFiles: kit.TrimNonEmpty(strings.Split(labels[compose.ConfigFilesLabelKey], ",")),
		envFiles:    kit.TrimNonEmpty(strings.Split(labels[compose.EnvironmentFileLabelKey], ",")),
		dockerHost:  dockerClient.DaemonHost(),
	}, nil
}
