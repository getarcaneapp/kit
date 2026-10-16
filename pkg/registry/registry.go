// Package registry canonicalizes container registry hosts so that image
// references and credential lookups agree on what "the same registry" means.
package registry

import (
	"net/url"
	"strings"

	kit "go.getarcane.app/kit/pkg"
)

// DefaultDomain is the canonical Docker Hub registry domain.
const DefaultDomain = "docker.io"

// Normalize canonicalizes a registry host or URL for equality checks; every
// Docker Hub alias collapses to "docker.io".
func Normalize(rawURL string) string {
	registryHost := strings.TrimSpace(rawURL)
	if parsed, err := url.Parse(registryHost); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		registryHost = parsed.Host + parsed.EscapedPath() + kit.Ternary(parsed.RawQuery != "", "?"+parsed.RawQuery, "")
	}
	registryHost, _, _ = strings.Cut(strings.ToLower(registryHost), "/")
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
	switch normalizedHost := Normalize(rawURL); normalizedHost {
	case "":
		return nil
	case DefaultDomain:
		return []string{DefaultDomain, "index.docker.io", "registry-1.docker.io"}
	default:
		return []string{normalizedHost}
	}
}
