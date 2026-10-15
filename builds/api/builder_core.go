package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	configtypes "github.com/docker/cli/cli/config/types"
	buildkit "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session/auth/authprovider"
	kit "go.getarcane.app/kit/pkg"
	"go.getarcane.app/kit/pkg/utils"

	dockerutils "go.getarcane.app/builds/pkg/docker"
	"go.getarcane.app/builds/types"
)

const defaultBuildTimeout = 30 * time.Minute

type Service struct {
	settings             types.SettingsProvider
	dockerClientProvider types.DockerClientProvider
	registryAuthProvider types.RegistryAuthProvider
	logger               *slog.Logger
	providers            map[string]any
}

// NewService constructs a build service.
func NewService(cfg Config) *Service {
	return &Service{
		settings:             cfg.SettingsProvider,
		dockerClientProvider: cfg.DockerClientProvider,
		registryAuthProvider: cfg.RegistryAuthProvider,
		logger:               cmp.Or(cfg.Logger, slog.Default()),
		providers: map[string]any{
			"depot": &depotBuildKitProvider{settings: cfg.SettingsProvider},
		},
	}
}

// NewBuilder constructs a build service from the legacy positional dependencies.
func NewBuilder(settings types.SettingsProvider, dockerClientProvider types.DockerClientProvider, registryAuthProvider types.RegistryAuthProvider) *Service {
	return NewService(Config{
		SettingsProvider:     settings,
		DockerClientProvider: dockerClientProvider,
		RegistryAuthProvider: registryAuthProvider,
	})
}

func (b *Service) BuildImage(ctx context.Context, req types.BuildRequest, progressWriter io.Writer, _ string) (*types.BuildResult, error) {
	if b.settings == nil {
		return nil, &types.BuildSettingsProviderUnavailableError{}
	}

	if strings.TrimSpace(req.ContextDir) == "" {
		return nil, &types.BuildContextDirRequiredError{}
	}

	settings := b.settings.BuildSettings()
	providerName := cmp.Or(strings.ToLower(strings.TrimSpace(req.Provider)), strings.ToLower(strings.TrimSpace(settings.BuildProvider)), "local")
	providerRaw, known := b.providers[providerName]
	if providerName != "local" && !known {
		return nil, fmt.Errorf("unknown build provider: %s", providerName)
	}
	provider, ok := providerRaw.(buildProvider)
	if providerName != "local" && !ok {
		return nil, fmt.Errorf("invalid build provider: %s", providerName)
	}

	buildCtx, cancel := context.WithTimeout(ctx, kit.Ternary(settings.BuildTimeoutSecs > 0, time.Duration(settings.BuildTimeoutSecs)*time.Second, defaultBuildTimeout))
	defer cancel()

	progressWriter = cmp.Or(progressWriter, io.Discard)

	// keep: req is a by-value copy; a full 22-field BuildRequest literal reads worse
	req.Tags = utils.NormalizeSet(req.Tags)
	req.Push = req.Push || (!req.Load && providerName == "depot")
	req.Load = req.Load || (!req.Push && providerName != "depot")

	err := validateBuildRequest(req, providerName)
	if err != nil {
		return nil, err
	}

	if providerName == "local" {
		requiresDirectBuildkitSession, requiresErr := requiresDirectLocalBuildkitSession(req)
		if requiresErr != nil {
			return nil, requiresErr
		}
		if !requiresDirectBuildkitSession {
			return b.buildWithDocker(buildCtx, req, progressWriter)
		}

		if b.dockerClientProvider == nil {
			return nil, &types.BuildDockerServiceUnavailableError{}
		}

		dockerClient, clientErr := b.dockerClientProvider.GetClient(buildCtx)
		if clientErr != nil {
			return nil, fmt.Errorf("failed to connect to Docker: %w", clientErr)
		}

		bk, bkErr := buildkit.New(buildCtx, "", dockerutils.ClientOpts(dockerClient)...)
		if bkErr != nil {
			return nil, fmt.Errorf("failed to connect to Docker BuildKit: %w", bkErr)
		}

		waitCtx, waitCancel := context.WithTimeout(buildCtx, 30*time.Second)
		waitErr := bk.Wait(waitCtx)
		waitCancel()
		if waitErr != nil {
			_ = bk.Close()
			return nil, fmt.Errorf("failed to wait for Docker BuildKit: %w", waitErr)
		}

		return b.buildWithBuildkitSession(buildCtx, req, progressWriter, providerName, &buildSession{
			Client: bk,
			Close: func(_ error) error {
				return bk.Close()
			},
		})
	}

	providerSession, err := provider.NewSession(buildCtx, req)
	if err != nil {
		return nil, err
	}

	return b.buildWithBuildkitSession(buildCtx, req, progressWriter, providerName, providerSession)
}

func (b *Service) buildWithBuildkitSession(
	ctx context.Context,
	req types.BuildRequest,
	progressWriter io.Writer,
	providerName string,
	bkSession *buildSession,
) (*types.BuildResult, error) {
	var buildErr error
	defer func() {
		if cerr := bkSession.Close(buildErr); cerr != nil {
			slog.WarnContext(ctx, "build session close error", "provider", providerName, "error", cerr)
		}
	}()

	solveOpt, loadErrCh, cleanupSolveOpt, err := b.buildSolveOpt(ctx, req, providerName)
	if err != nil {
		buildErr = err
		return nil, err
	}
	defer cleanupSolveOpt()

	statusCh := make(chan *buildkit.SolveStatus, 16)
	streamErrCh := make(chan error, 1)
	go func() {
		streamErrCh <- streamSolveStatus(ctx, statusCh, progressWriter)
	}()

	resp, err := bkSession.Client.Solve(ctx, nil, solveOpt, statusCh)
	if err != nil {
		err = wrapBuildkitSolveError(err, providerName)
		buildErr = err
		return nil, err
	}

	if streamErr := <-streamErrCh; streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		slog.WarnContext(ctx, "build progress stream error", "provider", providerName, "error", streamErr)
	}

	if loadErrCh != nil {
		if loadErr := <-loadErrCh; loadErr != nil {
			buildErr = loadErr
			return nil, loadErr
		}
	}

	if providerName == "local" && req.Push {
		dockerClient, dockerClientErr := b.dockerClientProvider.GetClient(ctx)
		if dockerClientErr != nil {
			buildErr = dockerClientErr
			return nil, dockerClientErr
		}
		if pushErr := b.pushDockerImages(ctx, dockerClient, req.Tags, progressWriter); pushErr != nil {
			buildErr = pushErr
			return nil, pushErr
		}
	}

	return &types.BuildResult{
		Provider: providerName,
		Tags:     req.Tags,
		Digest:   kit.FromPtr(resp).ExporterResponse["containerimage.digest"],
	}, nil
}

func wrapBuildkitSolveError(err error, providerName string) error {
	if err == nil {
		return nil
	}

	if strings.Contains(err.Error(), `exporter "docker" could not be found`) {
		return &types.BuildKitDockerExporterError{ProviderName: providerName, Err: err}
	}

	if strings.Contains(err.Error(), `exporter "image" could not be found`) {
		return &types.BuildKitImageExporterError{ProviderName: providerName, Err: err}
	}

	return err
}

func buildkitAuthConfigProvider(defaultProvider authprovider.AuthConfigProvider, registryAuthProvider types.RegistryAuthProvider) authprovider.AuthConfigProvider {
	return func(ctx context.Context, host string, scope []string, cacheCheck authprovider.ExpireCachedAuthCheck) (configtypes.AuthConfig, error) {
		if registryAuthProvider != nil {
			registryCfg, ok, err := registryAuthConfigForHost(ctx, registryAuthProvider, host)
			if err != nil {
				slog.WarnContext(ctx, "failed to resolve build registry auth from database, falling back to docker config", "registry", host, "error", err)
			} else if ok {
				return configtypes.AuthConfig{
					Username:      registryCfg.Username,
					Password:      registryCfg.Password,
					Auth:          registryCfg.Auth,
					ServerAddress: kit.Ternary(strings.TrimSpace(registryCfg.ServerAddress) == "", host, registryCfg.ServerAddress),
					IdentityToken: registryCfg.IdentityToken,
					RegistryToken: registryCfg.RegistryToken,
				}, nil
			}
		}

		return defaultProvider(ctx, host, scope, cacheCheck)
	}
}
