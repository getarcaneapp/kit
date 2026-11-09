package types

import "fmt"

type (
	BuildKitImageExporterError struct {
		Err          error
		ProviderName string
	}

	BuildKitDockerExporterError struct {
		Err          error
		ProviderName string
	}

	BuildSettingsProviderUnavailableError struct{}

	BuildContextDirRequiredError struct{}

	BuildProviderUnavailableError struct{}

	BuildSessionUnavailableError struct{}

	BuildDockerServiceUnavailableError struct{}

	DepotProjectCredentialsRequiredError struct{}

	GitBuildContextFragmentRequiredError struct{}

	GitBuildContextRefRequiredError struct{}

	GitBuildContextSubdirRequiredError struct{}

	GitBuildContextSubdirRelativeError struct{}

	GitBuildContextSubdirEscapesRepositoryError struct{}

	DockerfileAndInlineMutuallyExclusiveError struct{}

	DepotBuildPushRequiredError struct{}

	BuildTagsRequiredError struct{}

	DockerBuildMultiPlatformUnsupportedError struct{}
)

func (e *BuildKitImageExporterError) Error() string {
	return fmt.Sprintf("depot and remote BuildKit providers require the image exporter for provider %s: %v", e.ProviderName, e.Err)
}

func (e *BuildKitImageExporterError) Unwrap() error { return e.Err }

func (e *BuildKitDockerExporterError) Error() string {
	return fmt.Sprintf("the Docker Engine embedded BuildKit requires the docker image-store exporter (used for load) for provider %s: %v", e.ProviderName, e.Err)
}

func (e *BuildKitDockerExporterError) Unwrap() error { return e.Err }

func (e *BuildSettingsProviderUnavailableError) Error() string {
	return "settings provider not available"
}

func (e *BuildContextDirRequiredError) Error() string {
	return "contextDir is required"
}

func (e *BuildProviderUnavailableError) Error() string {
	return "build provider not available"
}

func (e *BuildSessionUnavailableError) Error() string {
	return "build session not available"
}

func (e *BuildDockerServiceUnavailableError) Error() string {
	return "docker service not available"
}

func (e *DepotProjectCredentialsRequiredError) Error() string {
	return "depot project ID and token are required"
}

func (e *GitBuildContextFragmentRequiredError) Error() string {
	return "git build context fragment cannot be empty"
}

func (e *GitBuildContextRefRequiredError) Error() string {
	return "git build context ref cannot be empty"
}

func (e *GitBuildContextSubdirRequiredError) Error() string {
	return "git build context subdir cannot be empty"
}

func (e *GitBuildContextSubdirRelativeError) Error() string {
	return "git build context subdir must be relative"
}

func (e *GitBuildContextSubdirEscapesRepositoryError) Error() string {
	return "git build context subdir must stay within the repository"
}

func (e *DockerfileAndInlineMutuallyExclusiveError) Error() string {
	return "dockerfile and dockerfileInline are mutually exclusive"
}

func (e *DepotBuildPushRequiredError) Error() string {
	return "depot builds must push images to a registry"
}

func (e *BuildTagsRequiredError) Error() string {
	return "at least one tag is required when push/load is enabled"
}

func (e *DockerBuildMultiPlatformUnsupportedError) Error() string {
	return "docker build fallback does not support multi-platform builds"
}
