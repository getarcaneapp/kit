package updater

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/types/container"
	"kit.getarcane.app"
)

// detachedTimeout bounds detached recreate and rollback work when Config.OperationTimeout is zero.
const detachedTimeout = 10 * time.Minute

type (
	// Service coordinates Docker image updates, container recreation, and the host adapters configured
	// through Config. A Service is safe for concurrent use.
	Service struct {
		logger *slog.Logger
		// ownedDockerClient is the default provider New built, the only one Close may shut down.
		ownedDockerClient  *DockerClient
		updatingContainers atomic.Pointer[[]string]
		updatingProjects   atomic.Pointer[[]string]
		config             Config
	}

	updatePlan struct {
		record ImageUpdateRecord
		oldRef string
		newRef string
		oldIDs []string
		pulled bool
	}

	restartPlan struct {
		inspect  *container.InspectResponse
		newRef   string
		match    string
		cnt      container.Summary
		implicit bool
	}
)

// New constructs an updater service; the zero Config is valid and uses the local Docker environment.
// Nil ports get their Docker-backed defaults and Config.LabelPolicy is merged over DefaultLabelPolicy.
func New(config Config) (*Service, error) {
	if config.OperationTimeout < 0 {
		return nil, &ConfigError{Field: "OperationTimeout", Reason: errors.New("must not be negative")}
	}
	owned := kit.Ternary(config.DockerClientProvider == nil, NewDockerClientProvider(), nil)
	provider := cmp.Or[DockerClientProvider](config.DockerClientProvider, owned)
	service := &Service{
		config: Config{
			DockerClientProvider:   provider,
			ImagePuller:            cmp.Or(config.ImagePuller, NewImagePuller(provider)),
			PendingStore:           cmp.Or(config.PendingStore, NewMemoryPendingStore()),
			RegistryDigestResolver: cmp.Or(config.RegistryDigestResolver, NewRegistryDigestResolver()),
			RegistryTagLister:      cmp.Or(config.RegistryTagLister, NewRegistryTagLister()),
			ProjectUpdater:         cmp.Or(config.ProjectUpdater, NewDockerComposeProjectUpdater(provider)),
			LabelPolicy:            DefaultLabelPolicy().Merge(config.LabelPolicy),
			RunRecorder:            config.RunRecorder,
			Settings:               config.Settings,
			SelfUpdater:            config.SelfUpdater,
			Notifier:               config.Notifier,
			EventRecorder:          config.EventRecorder,
			UsedImageCollector:     config.UsedImageCollector,
			Logger:                 config.Logger,
			SelfContainerID:        config.SelfContainerID,
			OperationTimeout:       config.OperationTimeout,
		},
		logger:            cmp.Or(config.Logger, slog.Default()),
		ownedDockerClient: owned,
	}
	service.updatingContainers.Store(&[]string{})
	service.updatingProjects.Store(&[]string{})
	return service, nil
}

// Close releases the Docker client New created; a caller-supplied provider is left to its owner. Close is idempotent.
func (s *Service) Close() error {
	if s == nil || s.ownedDockerClient == nil {
		return nil
	}
	return s.ownedDockerClient.Close()
}

func (s *Service) opCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if s == nil || s.config.OperationTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.config.OperationTimeout)
}

// detachedCtx outlives a caller cancel for work that must finish once started, such as restoring a
// stopped container. Each operation stays bounded by opCtx, or by detachedTimeout without OperationTimeout.
func (s *Service) detachedCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.config.OperationTimeout > 0 {
		return context.WithoutCancel(ctx), func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), detachedTimeout)
}
