// Package refs normalizes image references so every spelling of one image ("nginx", "nginx:latest",
// "docker.io/library/nginx:latest") compares equal, and flags references that cannot be updated.
package refs

import (
	"fmt"
	"strings"

	ref "github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	"go.getarcane.app/kit/pkg/registry"
)

// Reference is a normalized Docker image reference.
type Reference struct {
	NormalizedRef string
	RegistryHost  string
	Repository    string
	Tag           string
}

// NormalizeReference parses and canonicalizes an image reference.
func NormalizeReference(imageRef string) (*Reference, error) {
	trimmed := strings.TrimSpace(imageRef)
	if before, _, ok := strings.Cut(trimmed, "@"); ok {
		trimmed = before
	}

	named, err := ref.ParseNormalizedNamed(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference %q: %w", imageRef, err)
	}

	registryHost := registry.Normalize(ref.Domain(named))
	repository := ref.Path(named)

	tag := "latest"
	if tagged, ok := named.(ref.NamedTagged); ok {
		tag = tagged.Tag()
	}

	return &Reference{
		NormalizedRef: registryHost + "/" + repository + ":" + tag,
		RegistryHost:  registryHost,
		Repository:    repository,
		Tag:           tag,
	}, nil
}

// NormalizeImageUpdateRef returns the canonical image reference key used for update match.
func NormalizeImageUpdateRef(imageRef string) string {
	if IsDigestPinnedReference(imageRef) {
		return ""
	}
	parts, err := NormalizeReference(imageRef)
	if err != nil {
		return ""
	}
	return parts.NormalizedRef
}

// NormalizeImageUpdateRefMapKeys returns refToValue keyed by normalized reference, dropping keys that
// fail to normalize.
func NormalizeImageUpdateRefMapKeys(refToValue map[string]string) map[string]string {
	out := make(map[string]string, len(refToValue))
	for imageRef, value := range refToValue {
		if normalized := NormalizeImageUpdateRef(imageRef); normalized != "" {
			out[normalized] = value
		}
	}
	return out
}

// PreserveConfiguredRef returns targetRef spelled like configuredRef: the configured repository with the
// target's tag. Image IDs, digest-pinned targets, and other repositories come back unchanged.
func PreserveConfiguredRef(configuredRef, targetRef string) string {
	configuredRef = strings.TrimSpace(configuredRef)
	targetRef = strings.TrimSpace(targetRef)
	if configuredRef == "" || IsImageIDLikeReference(targetRef) || IsDigestPinnedReference(targetRef) {
		return targetRef
	}
	configured, err := NormalizeReference(configuredRef)
	if err != nil {
		return targetRef
	}
	target, err := NormalizeReference(targetRef)
	if err != nil || configured.RegistryHost != target.RegistryHost || configured.Repository != target.Repository {
		return targetRef
	}
	if configured.Tag == target.Tag && !IsDigestPinnedReference(configuredRef) {
		return configuredRef
	}
	name, _, _ := strings.Cut(configuredRef, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	return name + ":" + target.Tag
}

// IsImageIDLikeReference reports whether imageRef is a Docker image ID rather than a pullable tag.
func IsImageIDLikeReference(imageRef string) bool {
	imageRef = strings.ToLower(strings.TrimSpace(imageRef))
	if strings.HasPrefix(imageRef, "sha256:") {
		return true
	}
	if len(imageRef) != 64 {
		return false
	}
	for _, c := range imageRef {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// IsDigestPinnedReference reports whether imageRef names an immutable repository digest.
func IsDigestPinnedReference(imageRef string) bool {
	_, digestValue, ok := strings.Cut(strings.TrimSpace(imageRef), "@")
	if !ok {
		return false
	}
	_, err := digest.Parse(strings.TrimSpace(digestValue))
	return err == nil
}

// PullableImageRef picks the reference to pull for a container: its config
// image, its summary image, then its repo tags. Image IDs and digests never update, so "" means none.
func PullableImageRef(summaryImage, inspectConfigImage string, repoTags []string) string {
	for _, imageRef := range append([]string{inspectConfigImage, summaryImage}, repoTags...) {
		imageRef = strings.TrimSpace(imageRef)
		if imageRef != "" && imageRef != "<none>:<none>" && !IsImageIDLikeReference(imageRef) && !IsDigestPinnedReference(imageRef) {
			return imageRef
		}
	}
	return ""
}
