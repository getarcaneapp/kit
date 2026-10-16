package updater

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/moby/moby/api/types/container"
	kit "go.getarcane.app/kit/pkg"
)

// Service coordinates Docker image updates, container recreation, and the host adapters configured
// through Config. A Service is safe for concurrent use.
type Service struct {
	config Config
	logger *slog.Logger

	// ownedDockerClient is the default provider New built, the only one Close may shut down.
	ownedDockerClient *DockerClient

	updatingContainers atomic.Pointer[[]string]
	updatingProjects   atomic.Pointer[[]string]
}

type updatePlan struct {
	record ImageUpdateRecord
	oldRef string
	newRef string
	oldIDs []string
	pulled bool
}

type restartPlan struct {
	cnt      container.Summary
	inspect  *container.InspectResponse
	newRef   string
	match    string
	implicit bool
}

// New constructs an updater service; the zero Config is valid and uses the local Docker environment.
// Nil ports get their Docker-backed defaults and nil LabelPolicy funcs come from DefaultLabelPolicy.
func New(config Config) (*Service, error) {
	if config.OperationTimeout < 0 {
		return nil, &ConfigError{Field: "OperationTimeout", Reason: errors.New("must not be negative")}
	}
	owned := kit.Ternary(config.DockerClientProvider == nil, NewDockerClientProvider(), nil)
	defaults := DefaultLabelPolicy()
	// keep: by-value copy of the caller's Config; a full 17-field literal reads worse
	config.DockerClientProvider = cmp.Or[DockerClientProvider](config.DockerClientProvider, owned)
	config.ImagePuller = cmp.Or(config.ImagePuller, NewImagePuller(config.DockerClientProvider))
	config.PendingStore = cmp.Or(config.PendingStore, NewMemoryPendingStore())
	config.RegistryDigestResolver = cmp.Or(config.RegistryDigestResolver, NewRegistryDigestResolver())
	config.RegistryTagLister = cmp.Or(config.RegistryTagLister, NewRegistryTagLister())
	config.ProjectUpdater = cmp.Or(config.ProjectUpdater, NewDockerComposeProjectUpdater(config.DockerClientProvider))
	// Fill every nil func in the policy from DefaultLabelPolicy, field by field, so overriding one behavior
	// keeps the defaults for the rest.
	config.LabelPolicy = LabelPolicy{
		TagPolicyFunc:          kit.Ternary(config.LabelPolicy.TagPolicyFunc == nil, defaults.TagPolicyFunc, config.LabelPolicy.TagPolicyFunc),
		IsUpdateDisabledFunc:   kit.Ternary(config.LabelPolicy.IsUpdateDisabledFunc == nil, defaults.IsUpdateDisabledFunc, config.LabelPolicy.IsUpdateDisabledFunc),
		IsSelfUpdateTargetFunc: kit.Ternary(config.LabelPolicy.IsSelfUpdateTargetFunc == nil, defaults.IsSelfUpdateTargetFunc, config.LabelPolicy.IsSelfUpdateTargetFunc),
		IsAgentFunc:            kit.Ternary(config.LabelPolicy.IsAgentFunc == nil, defaults.IsAgentFunc, config.LabelPolicy.IsAgentFunc),
		IsServerFunc:           kit.Ternary(config.LabelPolicy.IsServerFunc == nil, defaults.IsServerFunc, config.LabelPolicy.IsServerFunc),
		IsSwarmTaskFunc:        kit.Ternary(config.LabelPolicy.IsSwarmTaskFunc == nil, defaults.IsSwarmTaskFunc, config.LabelPolicy.IsSwarmTaskFunc),
		StopSignalFunc:         kit.Ternary(config.LabelPolicy.StopSignalFunc == nil, defaults.StopSignalFunc, config.LabelPolicy.StopSignalFunc),
	}
	service := &Service{
		config:            config,
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
