// Package compose reads the Docker Compose labels the updater relies on to
// group containers into projects and services.
package compose

import "strings"

const (
	// ProjectLabelKey is Docker Compose's project label key.
	ProjectLabelKey = "com.docker.compose.project"
	// ServiceLabelKey is Docker Compose's service label key.
	ServiceLabelKey = "com.docker.compose.service"
	// WorkingDirLabelKey is Docker Compose's project directory label key.
	WorkingDirLabelKey = "com.docker.compose.project.working_dir"
	// ConfigFilesLabelKey is Docker Compose's comma-separated config files label key.
	ConfigFilesLabelKey = "com.docker.compose.project.config_files"
	// EnvironmentFileLabelKey is Docker Compose's comma-separated --env-file label key.
	EnvironmentFileLabelKey = "com.docker.compose.project.environment_file"
	// ImageLabelKey is the image content digest Compose compares on up.
	ImageLabelKey = "com.docker.compose.image"
	// ServiceContainerFilter excludes `docker compose run` one-off containers, as Compose does.
	ServiceContainerFilter = "com.docker.compose.oneoff=False"
)

// ProjectLabel returns the trimmed Docker Compose project label.
func ProjectLabel(labels map[string]string) string {
	return strings.TrimSpace(labels[ProjectLabelKey])
}

// ServiceLabel returns the trimmed Docker Compose service label.
func ServiceLabel(labels map[string]string) string {
	return strings.TrimSpace(labels[ServiceLabelKey])
}
