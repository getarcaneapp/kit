package types

import "fmt"

type BuildKitImageExporterError struct {
	ProviderName string
	Err          error
}

func (e *BuildKitImageExporterError) Error() string {
	return fmt.Sprintf("depot and remote BuildKit providers require the image exporter for provider %s: %v", e.ProviderName, e.Err)
}

func (e *BuildKitImageExporterError) Unwrap() error { return e.Err }

type BuildKitDockerExporterError struct {
	ProviderName string
	Err          error
}

func (e *BuildKitDockerExporterError) Error() string {
	return fmt.Sprintf("the Docker Engine embedded BuildKit requires the docker image-store exporter (used for load) for provider %s: %v", e.ProviderName, e.Err)
}

func (e *BuildKitDockerExporterError) Unwrap() error { return e.Err }

type BuildSettingsProviderUnavailableError struct{}

func (e *BuildSettingsProviderUnavailableError) Error() string {
	return "settings provider not available"
}

type BuildContextDirRequiredError struct{}

func (e *BuildContextDirRequiredError) Error() string {
	return "contextDir is required"
}

type BuildProviderUnavailableError struct{}

func (e *BuildProviderUnavailableError) Error() string {
	return "build provider not available"
}

type BuildSessionUnavailableError struct{}

func (e *BuildSessionUnavailableError) Error() string {
	return "build session not available"
}

type BuildDockerServiceUnavailableError struct{}

func (e *BuildDockerServiceUnavailableError) Error() string {
	return "docker service not available"
}

type DepotProjectCredentialsRequiredError struct{}

func (e *DepotProjectCredentialsRequiredError) Error() string {
	return "depot project ID and token are required"
}

type GitBuildContextFragmentRequiredError struct{}

func (e *GitBuildContextFragmentRequiredError) Error() string {
	return "git build context fragment cannot be empty"
}

type GitBuildContextRefRequiredError struct{}

func (e *GitBuildContextRefRequiredError) Error() string {
	return "git build context ref cannot be empty"
}

type GitBuildContextSubdirRequiredError struct{}

func (e *GitBuildContextSubdirRequiredError) Error() string {
	return "git build context subdir cannot be empty"
}

type GitBuildContextSubdirRelativeError struct{}

func (e *GitBuildContextSubdirRelativeError) Error() string {
	return "git build context subdir must be relative"
}

type GitBuildContextSubdirEscapesRepositoryError struct{}

func (e *GitBuildContextSubdirEscapesRepositoryError) Error() string {
	return "git build context subdir must stay within the repository"
}

type DockerfileAndInlineMutuallyExclusiveError struct{}

func (e *DockerfileAndInlineMutuallyExclusiveError) Error() string {
	return "dockerfile and dockerfileInline are mutually exclusive"
}

type DepotBuildPushRequiredError struct{}

func (e *DepotBuildPushRequiredError) Error() string {
	return "depot builds must push images to a registry"
}

type BuildTagsRequiredError struct{}

func (e *BuildTagsRequiredError) Error() string {
	return "at least one tag is required when push/load is enabled"
}

type DockerBuildMultiPlatformUnsupportedError struct{}

func (e *DockerBuildMultiPlatformUnsupportedError) Error() string {
	return "docker build fallback does not support multi-platform builds"
}
