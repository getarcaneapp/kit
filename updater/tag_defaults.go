package updater

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"

	"kit.getarcane.app/updater/refs"
	"kit.getarcane.app/updater/registry"
	"kit.getarcane.app/updater/types"
)

type (
	defaultRegistryTagLister struct{ httpClient *http.Client }
)

// NewRegistryTagLister lists registry tags using local Docker credentials.
func NewRegistryTagLister() types.RegistryTagLister { return defaultRegistryTagLister{} }

func (l defaultRegistryTagLister) ListTags(ctx context.Context, imageRef string) ([]string, error) {
	parsed, err := refs.NormalizeReference(imageRef)
	if err != nil {
		return nil, err
	}
	// With no usable credential this is the zero AuthConfig, which registry treats as anonymous.
	authConfig, _, err := defaultDockerConfigRegistryAuthConfig(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("registry auth: %w", err)
	}
	return registry.FetchTags(ctx, registry.Request{
		Credential: &authn.AuthConfig{
			Username:      authConfig.Username,
			Password:      authConfig.Password,
			IdentityToken: authConfig.IdentityToken,
			RegistryToken: authConfig.RegistryToken,
		},
		HTTPClient: l.httpClient,
		Host:       parsed.RegistryHost,
		Repository: parsed.Repository,
	})
}
