// Package buffer resolves optional channel buffer sizes for streams configs.
package buffer

// Size returns *size when it is set and non-negative, and fallback otherwise.
// A pointer distinguishes an explicit 0 (unbuffered) from "use the default".
func Size(size *int, fallback int) int {
	if size == nil || *size < 0 {
		return fallback
	}
	return *size
}
