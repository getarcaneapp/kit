package updater

import (
	"context"
	"log/slog"
	"net/http"

	"kit.getarcane.app/updater/registry"
	"kit.getarcane.app/updater/types"
)

type (
	defaultRegistryTagLister struct {
		httpClient *http.Client
		logger     *slog.Logger
	}
)

// NewRegistryTagLister lists registry tags using local Docker credentials.
func NewRegistryTagLister() types.RegistryTagLister { return defaultRegistryTagLister{} }

func (l defaultRegistryTagLister) ListTags(ctx context.Context, imageRef string) ([]string, error) {
	req, err := defaultRegistryRequest(ctx, l.logger, imageRef, l.httpClient)
	if err != nil {
		return nil, err
	}
	return registry.FetchTags(ctx, req)
}
