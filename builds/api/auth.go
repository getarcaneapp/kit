package api

import (
	"context"
	"fmt"
	"strings"

	ref "github.com/distribution/reference"
	"github.com/moby/moby/api/pkg/authconfig"
	dockerregistry "github.com/moby/moby/api/types/registry"
	kit "go.getarcane.app/kit/pkg"
	"go.getarcane.app/kit/pkg/registry"
)

func registryAuthConfigForHost(ctx context.Context, provider RegistryAuthProvider, host string) (dockerregistry.AuthConfig, bool, error) {
	authConfigs, err := provider.GetAllRegistryAuthConfigs(ctx)
	if err != nil {
		return dockerregistry.AuthConfig{}, false, err
	}

	for _, key := range registry.LookupKeys(host) {
		if cfg, ok := authConfigs[key]; ok {
			return cfg, true, nil
		}
	}

	return dockerregistry.AuthConfig{}, false, nil
}

func registryAuthHeaderForImage(ctx context.Context, provider RegistryAuthProvider, imageRef string) (string, error) {
	named, err := ref.ParseNormalizedNamed(imageRef)
	if err != nil {
		return "", err
	}
	registryHost := kit.Ternary(ref.Domain(named) == "docker.io", "index.docker.io", ref.Domain(named))

	cfg, ok, err := registryAuthConfigForHost(ctx, provider, registryHost)
	if err != nil || !ok {
		return "", err
	}

	encoded, err := authconfig.Encode(dockerregistry.AuthConfig{
		Username:      cfg.Username,
		Password:      cfg.Password,
		Auth:          cfg.Auth,
		ServerAddress: kit.Ternary(strings.TrimSpace(cfg.ServerAddress) == "", registryHost, cfg.ServerAddress),
		IdentityToken: cfg.IdentityToken,
		RegistryToken: cfg.RegistryToken,
	})
	if err != nil {
		return "", fmt.Errorf("encode registry auth header: %w", err)
	}
	return encoded, nil
}
