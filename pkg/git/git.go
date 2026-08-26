// Package git provides helpers for recognizing and normalizing Git
// repository URLs across the schemes Git supports (scp-like git@, git, ssh,
// http, and https).
package git

import (
	"net/url"
	"strings"
)

// IsSupportedRepositoryURL reports whether raw looks like a Git repository
// URL: an scp-like git@ address, or a git, ssh, http, or https URL with a
// host and a repository path.
func IsSupportedRepositoryURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}

	if strings.HasPrefix(trimmed, "git@") {
		return true
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}

	switch strings.ToLower(parsed.Scheme) {
	case "git", "ssh", "http", "https":
		return parsed.Host != "" && hasRepositoryPath(parsed.Path)
	default:
		return false
	}
}

// RequiresRemoteProbe reports whether raw is an http or https URL that could
// be either a Git repository or something else, so only asking the remote can
// settle it. URLs that already end in .git, and non-HTTP schemes, never need
// a probe.
func RequiresRemoteProbe(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "git@") {
		return false
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.Host != "" && hasRepositoryPath(parsed.Path) && !strings.HasSuffix(strings.ToLower(strings.TrimRight(parsed.Path, "/")), ".git")
	default:
		return false
	}
}

// NormalizeForMatch normalizes a repository URL for equality comparison by
// trimming whitespace and trailing slashes and dropping a .git suffix.
func NormalizeForMatch(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, "git@") {
		return strings.TrimSuffix(trimmed, ".git")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return strings.TrimSuffix(trimmed, ".git")
	}

	parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), ".git")
	return parsed.String()
}

func hasRepositoryPath(rawPath string) bool {
	trimmedPath := strings.TrimSpace(rawPath)
	return trimmedPath != "" && trimmedPath != "/"
}
