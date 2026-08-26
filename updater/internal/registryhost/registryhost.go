// Package registryhost resolves the registry hosts the Docker daemon expects
// for authentication. Canonicalizing hosts for equality checks lives in
// go.getarcane.app/kit/pkg/utils/registryhost.
package registryhost

import (
	"fmt"

	ref "github.com/distribution/reference"
)

const defaultRegistryDomain = "docker.io"
const defaultRegistryHost = "registry-1.docker.io"

// AuthAddress returns the Docker daemon auth address for an image reference.
func AuthAddress(imageRef string) (string, error) {
	named, err := ref.ParseNormalizedNamed(imageRef)
	if err != nil {
		return "", fmt.Errorf("parse image reference: %w", err)
	}
	addr := ref.Domain(named)
	if addr == defaultRegistryDomain {
		return defaultRegistryHost, nil
	}
	return addr, nil
}
