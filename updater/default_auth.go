package updater

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	dockerCliConfig "github.com/docker/cli/cli/config"
	dockerregistry "github.com/moby/moby/api/types/registry"

	"go.getarcane.app/updater/refs"
)

// defaultDockerConfigRegistryAuthConfig loads usable Docker config credentials for imageRef's registry,
// reporting false when the request should go out anonymously.
func defaultDockerConfigRegistryAuthConfig(ctx context.Context, imageRef string) (dockerregistry.AuthConfig, bool, error) {
	parsed, err := refs.NormalizeReference(imageRef)
	if err != nil {
		return dockerregistry.AuthConfig{}, false, fmt.Errorf("get registry address: %w", err)
	}
	configFile, err := dockerCliConfig.Load(strings.TrimSpace(os.Getenv(dockerCliConfig.EnvOverrideConfigDir)))
	if err != nil {
		return dockerregistry.AuthConfig{}, false, fmt.Errorf("load Docker config: %w", err)
	}

	authConfig, err := configFile.GetAuthConfig(parsed.RegistryHost)
	if err != nil {
		slog.DebugContext(ctx, "registry credentials unavailable; proceeding anonymously", "imageRef", imageRef, "reason", "credential lookup failed", "error", err)
		return dockerregistry.AuthConfig{}, false, nil
	}
	// docker/cli decodes "auth" into Username and Password when it loads the config.
	credential := dockerregistry.AuthConfig{
		Username:      strings.TrimSpace(authConfig.Username),
		Password:      strings.TrimSpace(authConfig.Password),
		Auth:          authConfig.Auth,
		ServerAddress: strings.TrimSpace(authConfig.ServerAddress),
		IdentityToken: strings.TrimSpace(authConfig.IdentityToken),
		RegistryToken: strings.TrimSpace(authConfig.RegistryToken),
	}
	if (credential.Username == "" || credential.Password == "") && credential.IdentityToken == "" && credential.RegistryToken == "" {
		slog.DebugContext(ctx, "registry credentials unavailable; proceeding anonymously", "imageRef", imageRef, "reason", "no usable username/password or token")
		return dockerregistry.AuthConfig{}, false, nil
	}
	return credential, true, nil
}
