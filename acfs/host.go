package acfs

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Host offers the Root operations on absolute host paths without confinement,
// following symbolic links like the os package. Use acfs.Host{}.
type Host struct{}

// cleanHostPath validates an absolute host path and returns its clean form.
func cleanHostPath(hostPath string) (string, error) {
	if hostPath == "" || !filepath.IsAbs(hostPath) || strings.ContainsRune(hostPath, '\x00') {
		return "", fmt.Errorf("%w: %q must be an absolute host path", ErrInvalidPath, hostPath)
	}
	cleaned := filepath.Clean(hostPath)
	if strings.Contains(cleaned, string(filepath.Separator)+temporaryWritePrefix) {
		return "", fmt.Errorf("%w: %q uses reserved ACFS name", ErrInvalidPath, hostPath)
	}
	return cleaned, nil
}
