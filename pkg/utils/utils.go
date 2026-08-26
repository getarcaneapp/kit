// Package utils provides focused helpers for working with string slices
// whose entries may be blank.
package utils

import (
	"sort"
	"strings"
)

// NormalizeSet trims every entry, drops blanks and duplicates, and returns
// the remaining entries sorted. The input slice is not modified.
func NormalizeSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

// CountNonEmpty returns the number of entries that are not blank after
// trimming whitespace.
func CountNonEmpty(values []string) int {
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}

// HasNonEmpty reports whether any entry is not blank after trimming
// whitespace.
func HasNonEmpty(values []string) bool {
	return CountNonEmpty(values) > 0
}
