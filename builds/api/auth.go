package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	ref "github.com/distribution/reference"
	dockerauthconfig "github.com/moby/moby/api/pkg/authconfig"
	dockerregistry "github.com/moby/moby/api/types/registry"
)

func registryAuthConfigForHostInternal(ctx context.Context, provider RegistryAuthProvider, host string) (dockerregistry.AuthConfig, bool, error) {
	if provider == nil {
		return dockerregistry.AuthConfig{}, false, nil
	}

	authConfigs, err := provider.GetAllRegistryAuthConfigs(ctx)
	if err != nil {
		return dockerregistry.AuthConfig{}, false, err
	}

	for _, key := range registryLookupKeysInternal(host) {
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

func normalizeRegistryForComparisonInternal(url string) string {
	url = strings.TrimSpace(strings.ToLower(url))
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimSuffix(url, "/")

	if slash := strings.Index(url, "/"); slash != -1 {
		url = url[:slash]
	}

	if url == "docker.io" || url == "registry-1.docker.io" || url == "index.docker.io" {
		return "docker.io"
	}
	return url
}

func registryLookupKeysInternal(url string) []string {
	normalizedHost := normalizeRegistryForComparisonInternal(url)
	if normalizedHost == "" {
		return nil
	}

	keys := map[string]struct{}{
		normalizedHost: {},
	}
	if normalizedHost == "docker.io" {
		keys["registry-1.docker.io"] = struct{}{}
		keys["index.docker.io"] = struct{}{}
	}

	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
