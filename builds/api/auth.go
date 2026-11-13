package api

import (
	"context"
	"fmt"
	"strings"

	ref "github.com/distribution/reference"
	"github.com/moby/moby/api/pkg/authconfig"
	dockerregistry "github.com/moby/moby/api/types/registry"
	"kit.getarcane.app"
	"kit.getarcane.app/registry"
)

func registryAuthConfigForHost(ctx context.Context, provider RegistryAuthProvider, host string) (dockerregistry.AuthConfig, bool, error) {
	authConfigs, err := provider.GetAllRegistryAuthConfigs(ctx)
	if err != nil {
		return dockerregistry.AuthConfig{}, false, err
	}
	cfg, ok := lookupRegistryAuth(authConfigs, host)
	return cfg, ok, nil
}

func lookupRegistryAuth(authConfigs map[string]dockerregistry.AuthConfig, host string) (dockerregistry.AuthConfig, bool) {
	for _, key := range registry.LookupKeys(host) {
		if cfg, ok := authConfigs[key]; ok {
			return cfg, true
		}
	}
	return dockerregistry.AuthConfig{}, false
}

func registryAuthHeaderForImage(authConfigs map[string]dockerregistry.AuthConfig, imageRef string) (string, error) {
	if len(authConfigs) == 0 {
		return "", nil
	}
	named, err := ref.ParseNormalizedNamed(imageRef)
	if err != nil {
		return "", err
	}
	registryHost := kit.Ternary(ref.Domain(named) == "docker.io", "index.docker.io", ref.Domain(named))

	cfg, ok := lookupRegistryAuth(authConfigs, registryHost)
	if !ok {
		return "", nil
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
