package api

import (
	"context"
	"fmt"
	"strings"

	ref "github.com/distribution/reference"
	dockerauthconfig "github.com/moby/moby/api/pkg/authconfig"
	dockerregistry "github.com/moby/moby/api/types/registry"
	"go.getarcane.app/kit/pkg/utils/registryhost"
)

func registryAuthConfigForHostInternal(ctx context.Context, provider RegistryAuthProvider, host string) (dockerregistry.AuthConfig, bool, error) {
	if provider == nil {
		return dockerregistry.AuthConfig{}, false, nil
	}

	authConfigs, err := provider.GetAllRegistryAuthConfigs(ctx)
	if err != nil {
		return dockerregistry.AuthConfig{}, false, err
	}

	for _, key := range registryhost.LookupKeys(host) {
		if cfg, ok := authConfigs[key]; ok {
			return cfg, true, nil
		}
	}

	return dockerregistry.AuthConfig{}, false, nil
}

func registryAuthHeaderForImageInternal(ctx context.Context, provider RegistryAuthProvider, imageRef string) (string, error) {
	registryHost, err := registryAddressInternal(imageRef)
	if err != nil {
		return "", err
	}

	cfg, ok, err := registryAuthConfigForHostInternal(ctx, provider, registryHost)
	if err != nil || !ok {
		return "", err
	}

	if strings.TrimSpace(cfg.ServerAddress) == "" {
		cfg.ServerAddress = registryHost
	}

	encoded, err := dockerauthconfig.Encode(cfg)
	if err != nil {
		return "", fmt.Errorf("encode registry auth header: %w", err)
	}
	return encoded, nil
}

func registryAddressInternal(imageRef string) (string, error) {
	named, err := ref.ParseNormalizedNamed(imageRef)
	if err != nil {
		return "", err
	}
	addr := ref.Domain(named)
	if addr == "docker.io" {
		return "index.docker.io", nil
	}
	return addr, nil
}
