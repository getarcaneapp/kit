// Package registry talks to container registries through go-containerregistry
// to list tags, resolve image digests and read pull rate limits.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"

	kitregistry "go.getarcane.app/kit/pkg/registry"
)

const (
	defaultRegistryHost           = "registry-1.docker.io"
	daemonProxyConnectIndicator   = "proxy" + "connect"
	registryRateLimitHeaderSource = "rate" + "limit"
)

// Credentials contains registry credentials used for manifest requests.
type Credentials struct {
	Username string
	Token    string
	// IdentityToken and RegistryToken are OAuth tokens from a Docker config.
	IdentityToken string
	RegistryToken string
}

// RateLimitInfo contains pull quota information returned by registry headers.
type RateLimitInfo struct {
	Limit         *int   `json:"limit,omitempty"`
	Remaining     *int   `json:"remaining,omitempty"`
	Used          *int   `json:"used,omitempty"`
	WindowSeconds *int   `json:"windowSeconds,omitempty"`
	Source        string `json:"source,omitempty"`
}

// NewHTTPClient creates the default registry HTTP client.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// IsFallbackEligibleDaemonError reports whether a daemon registry error should use direct HTTP fallback.
func IsFallbackEligibleDaemonError(err error) bool {
	if err == nil {
		return false
	}

	errLower := strings.ToLower(err.Error())
	for _, blocked := range []string{
		"unauthorized", "authentication required", "no basic auth credentials", "access denied",
		"incorrect username or password", "status: 401", "status 401", "x509", "certificate", "tls",
	} {
		if strings.Contains(errLower, blocked) {
			return false
		}
	}

	for _, indicator := range []string{
		"not found", " 404 ", "status: 404", "status 404", "403 forbidden", "status: 403",
		"status 403", "administrative rules", "not implemented", "unsupported",
		"distribution disabled", "distribution api", daemonProxyConnectIndicator,
	} {
		if strings.Contains(errLower, indicator) {
			return true
		}
	}
	return false
}

// FetchRegistryRateLimit fetches registry pull rate-limit information.
func FetchRegistryRateLimit(ctx context.Context, registryHost, repository, tag string, credential *Credentials, httpClient *http.Client) (*RateLimitInfo, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ref, err := manifestReferenceInternal(registryHost, repository, tag)
	if err != nil {
		return nil, err
	}
	repo := ref.Context()
	authorized, err := transport.NewWithContext(requestCtx, repo.Registry, authenticatorInternal(credential), baseTransportInternal(httpClient), []string{repo.Scope(transport.PullScope)})
	if err != nil {
		return nil, fmt.Errorf("authorize registry: %w", err)
	}

	// HEAD reads the rate-limit headers without counting as a pull.
	manifestURL := url.URL{Scheme: repo.Scheme(), Host: repo.RegistryStr(), Path: "/v2/" + repo.RepositoryStr() + "/manifests/" + ref.TagStr()}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodHead, manifestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", strings.Join([]string{
		string(types.OCIImageIndex),
		string(types.OCIManifestSchema1),
		string(types.DockerManifestList),
		string(types.DockerManifestSchema2),
	}, ", "))
	resp, err := (&http.Client{Transport: authorized}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := transport.CheckError(resp, http.StatusOK); err != nil {
		return nil, err
	}
	return extractRateLimitFromHeaders(resp.Header)
}

// FetchDigest fetches the manifest digest for a registry image reference.
func FetchDigest(ctx context.Context, registryHost, repository, tag string, credential *Credentials, httpClient *http.Client) (string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ref, err := manifestReferenceInternal(registryHost, repository, tag)
	if err != nil {
		return "", err
	}
	options := remoteOptionsInternal(requestCtx, credential, httpClient)

	// HEAD does not count as a pull. Fall back to GET only when the registry
	// answered without a digest header, since GET computes it from the manifest.
	desc, err := remote.Head(ref, options...)
	var registryErr *transport.Error
	if err != nil && !errors.As(err, &registryErr) && requestCtx.Err() == nil {
		var manifest *remote.Descriptor
		if manifest, err = remote.Get(ref, options...); err == nil {
			desc = &manifest.Descriptor
		}
	}
	if err != nil {
		return "", fmt.Errorf("fetch manifest digest: %w", err)
	}
	return desc.Digest.String(), nil
}

func repositoryInternal(registryHost, repository string) (name.Repository, error) {
	registryHost = kitregistry.Normalize(registryHost)
	if registryHost == "docker.io" {
		registryHost = defaultRegistryHost
	}
	repo, err := name.NewRepository(registryHost + "/" + strings.Trim(repository, "/"))
	if err != nil {
		return name.Repository{}, fmt.Errorf("parse registry repository: %w", err)
	}
	return repo, nil
}

func manifestReferenceInternal(registryHost, repository, tag string) (name.Tag, error) {
	repo, err := repositoryInternal(registryHost, repository)
	if err != nil {
		return name.Tag{}, err
	}
	ref, err := name.NewTag(repo.Name() + ":" + strings.TrimSpace(tag))
	if err != nil {
		return name.Tag{}, fmt.Errorf("parse image tag: %w", err)
	}
	return ref, nil
}

func remoteOptionsInternal(ctx context.Context, credential *Credentials, httpClient *http.Client) []remote.Option {
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(authenticatorInternal(credential)),
		remote.WithTransport(baseTransportInternal(httpClient)),
	}
}

func baseTransportInternal(httpClient *http.Client) http.RoundTripper {
	if httpClient != nil && httpClient.Transport != nil {
		return httpClient.Transport
	}
	return http.DefaultTransport
}

func authenticatorInternal(credential *Credentials) authn.Authenticator {
	if credential == nil {
		return authn.Anonymous
	}
	config := authn.AuthConfig{
		IdentityToken: strings.TrimSpace(credential.IdentityToken),
		RegistryToken: strings.TrimSpace(credential.RegistryToken),
	}
	if username, token := strings.TrimSpace(credential.Username), strings.TrimSpace(credential.Token); username != "" && token != "" {
		config.Username, config.Password = username, token
	}
	if config == (authn.AuthConfig{}) {
		return authn.Anonymous
	}
	return authn.FromConfig(config)
}

func extractRateLimitFromHeaders(header http.Header) (*RateLimitInfo, error) {
	info := &RateLimitInfo{}
	if limit, window := parseRateLimitHeader(header.Get("Ratelimit-Limit")); limit != nil {
		info.Limit = limit
		info.WindowSeconds = window
		info.Source = registryRateLimitHeaderSource
	}
	if remaining, window := parseRateLimitHeader(header.Get("Ratelimit-Remaining")); remaining != nil {
		info.Remaining = remaining
		if info.WindowSeconds == nil {
			info.WindowSeconds = window
		}
		info.Source = registryRateLimitHeaderSource
	}
	if used, err := strconv.Atoi(strings.TrimSpace(header.Get("Docker-Ratelimit-Used"))); err == nil {
		info.Used = &used
		if info.Source == "" {
			info.Source = "docker"
		}
	}
	if info.Limit == nil && info.Remaining == nil && info.Used == nil {
		return nil, errors.New("no registry rate limit headers found")
	}
	return info, nil
}

func parseRateLimitHeader(value string) (*int, *int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	first, rest, _ := strings.Cut(value, ";")
	n, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return nil, nil
	}

	var window *int
	for part := range strings.SplitSeq(rest, ";") {
		key, rawValue, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.ToLower(key) != "w" {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(rawValue))
		if err == nil {
			window = &parsed
		}
	}
	return &n, window
}
