package updater

import (
	"context"
	"fmt"
	"net/http"

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
	credential, err := defaultDigestCredentials(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("registry auth: %w", err)
	}
	return registry.FetchTags(ctx, parsed.RegistryHost, parsed.Repository, credential, l.httpClient)
}
