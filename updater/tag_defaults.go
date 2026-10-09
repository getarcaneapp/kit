package updater

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"

	"go.getarcane.app/updater/refs"
	"go.getarcane.app/updater/registry"
	"go.getarcane.app/updater/types"
)

type defaultRegistryTagLister struct{ httpClient *http.Client }

// NewRegistryTagLister lists registry tags using local Docker credentials.
func NewRegistryTagLister() types.RegistryTagLister { return defaultRegistryTagLister{} }

func (l defaultRegistryTagLister) ListTags(ctx context.Context, imageRef string) ([]string, error) {
	parsed, err := refs.NormalizeReference(imageRef)
	if err != nil {
		return nil, err
	}
	authConfig, ok, err := defaultDockerConfigRegistryAuthConfig(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("registry auth: %w", err)
	}
	var credential *authn.AuthConfig
	if ok {
		credential = &authn.AuthConfig{Username: authConfig.Username, Password: authConfig.Password, IdentityToken: authConfig.IdentityToken, RegistryToken: authConfig.RegistryToken}
	}
	return registry.FetchTags(ctx, parsed.RegistryHost, parsed.Repository, credential, l.httpClient)
}
