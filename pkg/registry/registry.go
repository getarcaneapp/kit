// Package registry canonicalizes container registry hosts so that image
// references and credential lookups agree on what "the same registry" means.
package registry

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

// DefaultDomain is the canonical Docker Hub registry domain.
const DefaultDomain = "docker.io"

// Normalize canonicalizes a registry host or URL for equality checks; every
// Docker Hub alias collapses to "docker.io".
func Normalize(rawURL string) string {
	registryHost := strings.ToLower(stripScheme(rawURL))
	if slash := strings.Index(registryHost, "/"); slash != -1 {
		registryHost = registryHost[:slash]
	}
	switch registryHost {
	case DefaultDomain, "registry-1.docker.io", "index.docker.io":
		return DefaultDomain
	default:
		return registryHost
	}
}

// LookupKeys returns the sorted set of host keys a credential store should be
// probed with for rawURL. Docker Hub expands to all of its aliases; an
// unrecognizable host yields nil.
func LookupKeys(rawURL string) []string {
	normalizedHost := Normalize(rawURL)
	if normalizedHost == "" {
		return nil
	}

	keys := map[string]struct{}{
		normalizedHost: {},
	}
	if normalizedHost == DefaultDomain {
		keys["registry-1.docker.io"] = struct{}{}
		keys["index.docker.io"] = struct{}{}
	}

	return slices.Sorted(maps.Keys(keys))
}

func stripScheme(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSuffix(rawURL, "/")
	}

	result := parsed.Host
	if path := parsed.EscapedPath(); path != "" {
		result += path
	}
	if parsed.RawQuery != "" {
		result += "?" + parsed.RawQuery
	}
	return strings.TrimSuffix(result, "/")
}
