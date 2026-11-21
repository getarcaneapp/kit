// Package compose defines the additional Docker Compose label keys the updater
// relies on beyond the project and service labels in kit.getarcane.app/docker.
package compose

const (
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
