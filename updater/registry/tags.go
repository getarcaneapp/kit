package registry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	tagsPageSize            = 1000
	defaultTagsFetchTimeout = 120 * time.Second
)

// FetchTags lists all repository tags, including paginated results. A partial
// listing is never returned on failure. The caller's deadline bounds the whole
// walk; without one the lookup falls back to a 120-second limit.
func FetchTags(
	ctx context.Context,
	registryHost, repository string,
	credential *authn.AuthConfig,
	httpClient *http.Client,
) ([]string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTagsFetchTimeout)
		defer cancel()
	}

	repo, err := repositoryInternal(registryHost, repository)
	if err != nil {
		return nil, err
	}
	tags, err := remote.List(repo, append(remoteOptionsInternal(ctx, credential, httpClient), remote.WithPageSize(tagsPageSize))...)
	if err != nil {
		return nil, fmt.Errorf("list registry tags: %w", err)
	}
	return tags, nil
}
