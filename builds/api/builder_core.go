package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/docker/cli/cli/config"
	configtypes "github.com/docker/cli/cli/config/types"
	buildkit "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session/auth/authprovider"

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
func NewService(config Config) *Service {
	providers := map[string]any{
		"depot": newDepotBuildKitProviderInternal(config.SettingsProvider),
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		settings:             config.SettingsProvider,
		dockerClientProvider: config.DockerClientProvider,
		registryAuthProvider: config.RegistryAuthProvider,
		logger:               logger,
		providers:            providers,
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

func (b *Service) BuildImage(ctx context.Context, req types.BuildRequest, progressWriter io.Writer, serviceName string) (*types.BuildResult, error) {
	if b.settings == nil {
		return nil, &types.BuildSettingsProviderUnavailableError{}
	}

	if strings.TrimSpace(req.ContextDir) == "" {
		return nil, &types.BuildContextDirRequiredError{}
	}

	settings := b.settings.BuildSettings()
	providerName, provider, err := b.resolveProviderInternal(req.Provider, settings.BuildProvider)
	if err != nil {
		return nil, err
	}

	buildCtx, cancel := context.WithTimeout(ctx, buildTimeoutDurationInternal(settings.BuildTimeoutSecs))
	defer cancel()

	req = normalizeBuildRequestInternal(req, providerName)
	req.Tags = normalizeTagsInternal(req.Tags)

	if err := validateBuildRequestInternal(req, providerName); err != nil {
		return nil, err
	}

	if providerName == "local" {
		requiresBuildkit, err := requiresLocalBuildkitInternal(req)
		if err != nil {
			return nil, err
		}
		if requiresBuildkit {
			session, err := b.newLocalBuildkitSessionInternal(buildCtx)
			if err != nil {
				return nil, err
			}
			return b.buildWithBuildkitSessionInternal(buildCtx, req, progressWriter, serviceName, providerName, session)
		}
		return b.buildWithDockerInternal(buildCtx, req, progressWriter, serviceName)
	}

	if provider == nil {
		return nil, &types.BuildProviderUnavailableError{}
	}

	session, err := provider.NewSession(buildCtx, req)
	if err != nil {
		return nil, err
	}

	return b.buildWithBuildkitSessionInternal(buildCtx, req, progressWriter, serviceName, providerName, session)
}

func (b *Service) buildWithBuildkitSessionInternal(
	ctx context.Context,
	req types.BuildRequest,
	progressWriter io.Writer,
	serviceName string,
	providerName string,
	session *buildSession,
) (*types.BuildResult, error) {
	if session == nil || session.Client == nil {
		return nil, &types.BuildSessionUnavailableError{}
	}

	var buildErr error
	defer func() {
		if cerr := session.Close(buildErr); cerr != nil {
			slog.WarnContext(ctx, "build session close error", "provider", providerName, "error", cerr)
		}
	}()

	solveOpt, loadErrCh, cleanupSolveOpt, err := b.buildSolveOptInternal(ctx, req, providerName)
	if err != nil {
		buildErr = err
		return nil, err
	}
	defer cleanupSolveOpt()

	authProvider := authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: buildkitAuthConfigProviderInternal(authprovider.LoadAuthConfig(config.LoadDefaultConfigFile(os.Stderr)), b.registryAuthProvider),
	})
	solveOpt.Session = append(solveOpt.Session, authProvider)

	statusCh := make(chan *buildkit.SolveStatus, 16)
	streamErrCh := make(chan error, 1)
	go func() {
		streamErrCh <- streamSolveStatusInternal(ctx, statusCh, progressWriter, serviceName)
	}()

	writeProgressEventInternal(progressWriter, types.ProgressEvent{
		Type:    "build",
		Phase:   "begin",
		Service: serviceName,
		Status:  "build started",
	})

	resp, err := session.Client.Solve(ctx, nil, solveOpt, statusCh)

	if err != nil {
		err = wrapBuildkitSolveErrorInternal(err, providerName)
		buildErr = err
		writeProgressEventInternal(progressWriter, types.ProgressEvent{
			Type:    "build",
			Service: serviceName,
			Error:   err.Error(),
		})
		return nil, err
	}

	if streamErr := <-streamErrCh; streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		slog.WarnContext(ctx, "build progress stream error", "provider", providerName, "error", streamErr)
	}

	if loadErrCh != nil {
		if loadErr := <-loadErrCh; loadErr != nil {
			buildErr = loadErr
			writeProgressEventInternal(progressWriter, types.ProgressEvent{
				Type:    "build",
				Service: serviceName,
				Error:   loadErr.Error(),
			})
			return nil, loadErr
		}
	}

	if providerName == "local" && req.Push {
		if b.dockerClientProvider == nil {
			missingClientErr := &types.BuildDockerServiceUnavailableError{}
			buildErr = missingClientErr
			writeProgressEventInternal(progressWriter, types.ProgressEvent{
				Type:    "build",
				Service: serviceName,
				Error:   missingClientErr.Error(),
			})
			return nil, missingClientErr
		}

		dockerClient, dockerClientErr := b.dockerClientProvider.GetClient(ctx)
		if dockerClientErr != nil {
			buildErr = dockerClientErr
			writeProgressEventInternal(progressWriter, types.ProgressEvent{
				Type:    "build",
				Service: serviceName,
				Error:   dockerClientErr.Error(),
			})
			return nil, dockerClientErr
		}
		if pushErr := b.pushDockerImagesInternal(ctx, dockerClient, req.Tags, progressWriter, serviceName); pushErr != nil {
			buildErr = pushErr
			writeProgressEventInternal(progressWriter, types.ProgressEvent{
				Type:    "build",
				Service: serviceName,
				Error:   pushErr.Error(),
			})
			return nil, pushErr
		}
	}

	writeProgressEventInternal(progressWriter, types.ProgressEvent{
		Type:    "build",
		Phase:   "complete",
		Service: serviceName,
		Status:  "build complete",
	})

	digest := ""
	if resp != nil {
		if v, ok := resp.ExporterResponse["containerimage.digest"]; ok {
			digest = v
		}
	}

	return &types.BuildResult{
		Provider: providerName,
		Tags:     req.Tags,
		Digest:   digest,
	}, nil
}

func wrapBuildkitSolveErrorInternal(err error, providerName string) error {
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

func buildkitAuthConfigProviderInternal(defaultProvider authprovider.AuthConfigProvider, registryAuthProvider types.RegistryAuthProvider) authprovider.AuthConfigProvider {
	return func(ctx context.Context, host string, scope []string, cacheCheck authprovider.ExpireCachedAuthCheck) (configtypes.AuthConfig, error) {
		if registryAuthProvider != nil {
			registryCfg, ok, err := registryAuthConfigForHostInternal(ctx, registryAuthProvider, host)
			if err != nil {
				slog.WarnContext(ctx, "failed to resolve build registry auth from database, falling back to docker config", "registry", host, "error", err)
			} else if ok {
				authConfig := configtypes.AuthConfig{
					Username:      registryCfg.Username,
					Password:      registryCfg.Password,
					Auth:          registryCfg.Auth,
					ServerAddress: registryCfg.ServerAddress,
					IdentityToken: registryCfg.IdentityToken,
					RegistryToken: registryCfg.RegistryToken,
				}
				if strings.TrimSpace(authConfig.ServerAddress) == "" {
					authConfig.ServerAddress = host
				}
				return authConfig, nil
			}
		}

		if defaultProvider == nil {
			return configtypes.AuthConfig{}, nil
		}

		return defaultProvider(ctx, host, scope, cacheCheck)
	}
}

func buildTimeoutDurationInternal(settingSeconds int) time.Duration {
	if settingSeconds > 0 {
		return time.Duration(settingSeconds) * time.Second
	}
	return defaultBuildTimeout
}

func (b *Service) resolveProviderInternal(override string, defaultProvider string) (string, buildProvider, error) {
	providerName := strings.ToLower(strings.TrimSpace(override))
	if providerName == "" {
		providerName = strings.ToLower(strings.TrimSpace(defaultProvider))
	}
	if providerName == "" {
		providerName = "local"
	}
	if providerName == "local" {
		return providerName, nil, nil
	}
	providerRaw, ok := b.providers[providerName]
	if !ok {
		return "", nil, fmt.Errorf("unknown build provider: %s", providerName)
	}
	provider, ok := providerRaw.(buildProvider)
	if !ok || provider == nil {
		return "", nil, fmt.Errorf("invalid build provider: %s", providerName)
	}
	return providerName, provider, nil
}
