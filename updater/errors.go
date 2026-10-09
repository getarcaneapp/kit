package updater

import (
	"errors"
	"fmt"
)

var (
	// ErrDockerClientProviderRequired is returned when no Docker client
	// provider is configured.
	ErrDockerClientProviderRequired = errors.New("docker client provider is required")
	// ErrImagePullerRequired is returned when an update needs to pull an image
	// but no image puller is configured.
	ErrImagePullerRequired = errors.New("image puller is required")
	// ErrPendingStoreRequired is returned by ApplyPending when no pending store
	// is configured.
	ErrPendingStoreRequired = errors.New("pending store is required")
	// ErrSelfUpdaterRequired is returned when a container must be handled by
	// the host application but no SelfUpdater is configured.
	ErrSelfUpdaterRequired = errors.New("self-update requires a SelfUpdater")
	// ErrSelfUpdateContainer is returned when a self-update target reaches the
	// standalone recreate path, which must never recreate it.
	ErrSelfUpdateContainer = errors.New("self-update containers must use SelfUpdater")
	// ErrNilDockerClient is returned when the configured provider hands back a
	// nil Docker client.
	ErrNilDockerClient = errors.New("docker client unavailable")
	// ErrContainerNotFound is returned when the requested container does not
	// exist. Use errors.Is to detect it; the returned error names the container.
	ErrContainerNotFound = errors.New("container not found")
)

// ConfigError reports a Config field that New could not accept.
type ConfigError struct {
	// Field is the Config field at fault, as written in Go source.
	Field string
	// Reason is the underlying cause.
	Reason error
}

// Error implements error.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("updater config: %s: %v", e.Field, e.Reason)
}

// Unwrap returns the underlying cause, so errors.Is can match the sentinel.
func (e *ConfigError) Unwrap() error {
	return e.Reason
}
