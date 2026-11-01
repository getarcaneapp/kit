package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	tagsPageSize            = 1000
	defaultTagsFetchTimeout = 120 * time.Second
)

// FetchTags lists every repository tag across pages, never returning a partial listing. The caller's
// deadline bounds the walk, defaulting to 120 seconds.
func FetchTags(ctx context.Context, req Request) ([]string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTagsFetchTimeout)
		defer cancel()
	}

	repo, err := parseRepository(req.Host, req.Repository)
	if err != nil {
		return nil, err
	}
	tags, err := remote.List(repo, remote.WithContext(ctx), remote.WithAuth(authenticator(req.Credential)), remote.WithTransport(baseTransport(req.HTTPClient)), remote.WithPageSize(tagsPageSize))
	if err != nil {
		return nil, fmt.Errorf("list registry tags: %w", err)
	}
	return tags, nil
}
