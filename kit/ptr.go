package kit

// FromPtr returns the pointed-to value, or T's zero value if ptr is nil.
func FromPtr[T any](ptr *T) T {
	if ptr == nil {
		var zero T
		return zero
	}
	return *ptr
}
