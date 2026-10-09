package updater

import (
	"net"
	"net/url"
	"slices"
	"strings"
)

// isExcluded reports whether the settings exclusions name a container by ID or by any of its names.
func isExcluded(excluded map[string]bool, id string, names ...string) bool {
	return excluded[id] || slices.ContainsFunc(names, func(name string) bool { return excluded[strings.TrimPrefix(name, "/")] })
}

// dockerProxyContainerName returns the container name a tcp DOCKER_HOST points at, or "" for anything else.
func dockerProxyContainerName(dockerHost string) string {
	u, err := url.Parse(strings.TrimSpace(dockerHost))
	if err != nil || !strings.EqualFold(u.Scheme, "tcp") {
		return ""
	}
	host := u.Hostname()
	if host == "localhost" || strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return ""
	}
	return host
}
