package updater

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/authn"
	dockerauthconfig "github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/client"

	"go.getarcane.app/updater/refs"
	"go.getarcane.app/updater/registry"
)

// DockerClient is the built-in DockerClientProvider: it lazily opens one client, pings it on every
// hand-out, and reconnects after the daemon goes away. Construct one with NewDockerClientProvider.
type DockerClient struct {
	options []client.Opt
	client  atomic.Pointer[client.Client]
}

type defaultImagePuller struct {
	dockerClientProvider DockerClientProvider
}

type defaultRegistryDigestResolver struct {
	httpClient *http.Client
}

// NewDockerClientProvider returns a provider for the local Docker environment plus options. The caller
// owns it and should Close it, unless New built it, in which case Service.Close does.
func NewDockerClientProvider(options ...client.Opt) *DockerClient {
	return &DockerClient{options: append([]client.Opt{client.FromEnv}, options...)}
}

// NewImagePuller returns an image puller backed by Docker's ImagePull API.
func NewImagePuller(provider DockerClientProvider) ImagePuller {
	return defaultImagePuller{dockerClientProvider: cmp.Or[DockerClientProvider](provider, NewDockerClientProvider())}
}

// NewRegistryDigestResolver returns a registry HTTP digest resolver.
func NewRegistryDigestResolver() RegistryDigestResolver {
	return defaultRegistryDigestResolver{}
}

// DockerClient returns a live Docker client, opening or reopening one as
// needed. It implements DockerClientProvider.
func (p *DockerClient) DockerClient(ctx context.Context) (*client.Client, error) {
	if p == nil {
		return nil, ErrDockerClientProviderRequired
	}
	if dockerClient := p.client.Load(); dockerClient != nil {
		if _, err := dockerClient.Ping(ctx, client.PingOptions{}); err != nil {
			var closeErr error
			if p.client.CompareAndSwap(dockerClient, nil) {
				closeErr = dockerClient.Close()
			}
			return nil, fmt.Errorf("ping docker daemon: %w", errors.Join(err, closeErr))
		}
		return dockerClient, nil
	}

	dockerClient, err := client.New(p.options...)
	if err != nil {
		return nil, err
	}
	if _, err = dockerClient.Ping(ctx, client.PingOptions{}); err != nil {
		return nil, fmt.Errorf("ping docker daemon: %w", errors.Join(err, dockerClient.Close()))
	}
	if p.client.CompareAndSwap(nil, dockerClient) {
		return dockerClient, nil
	}
	winner := p.client.Load()
	if closeErr := dockerClient.Close(); closeErr != nil {
		return nil, fmt.Errorf("close unused docker client: %w", closeErr)
	}
	if winner == nil {
		return p.DockerClient(ctx)
	}
	return winner, nil
}

// Close shuts down the Docker client, if one is open. It is safe to call more
// than once, and a later DockerClient call opens a fresh connection.
func (p *DockerClient) Close() error {
	if p == nil {
		return nil
	}
	dockerClient := p.client.Swap(nil)
	if dockerClient == nil {
		return nil
	}
	return dockerClient.Close()
}

func (p defaultImagePuller) PullImage(ctx context.Context, imageRef string, progress io.Writer) error {
	dockerClient, err := p.dockerClientProvider.DockerClient(ctx)
	if err != nil {
		return fmt.Errorf("docker connect: %w", err)
	}
	authConfig, ok, err := defaultDockerConfigRegistryAuthConfig(ctx, imageRef)
	if err != nil {
		return fmt.Errorf("registry auth: %w", err)
	}
	var pullOptions client.ImagePullOptions
	if ok {
		if pullOptions.RegistryAuth, err = dockerauthconfig.Encode(authConfig); err != nil {
			return fmt.Errorf("registry auth: encode registry auth: %w", err)
		}
		// Retry anonymously when the registry rejects the credentials.
		pullOptions.PrivilegeFunc = func(context.Context) (string, error) { return "", nil }
	}
	resp, err := dockerClient.ImagePull(ctx, imageRef, pullOptions)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Close() }()

	for msg, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return err
		}
		if progress == nil {
			continue
		}
		if writeErr := json.MarshalWrite(progress, msg); writeErr != nil {
			_ = resp.Close()
			return fmt.Errorf("write pull progress: %w", writeErr)
		}
		if _, writeErr := io.WriteString(progress, "\n"); writeErr != nil {
			_ = resp.Close()
			return fmt.Errorf("terminate pull progress: %w", writeErr)
		}
	}
	return nil
}

func (r defaultRegistryDigestResolver) ImageDigest(ctx context.Context, imageRef string) (string, error) {
	parsed, err := refs.NormalizeReference(imageRef)
	if err != nil {
		return "", err
	}
	authConfig, ok, err := defaultDockerConfigRegistryAuthConfig(ctx, imageRef)
	if err != nil {
		return "", fmt.Errorf("registry auth: %w", err)
	}
	var credential *authn.AuthConfig
	if ok {
		credential = &authn.AuthConfig{Username: authConfig.Username, Password: authConfig.Password, IdentityToken: authConfig.IdentityToken, RegistryToken: authConfig.RegistryToken}
	}
	return registry.FetchDigest(ctx, parsed.RegistryHost, parsed.Repository, parsed.Tag, credential, r.httpClient)
}
