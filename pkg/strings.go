package kit

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ToString converts any value to a trimmed string.
func ToString(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(val)
	case fmt.Stringer:
		return strings.TrimSpace(val.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// Capitalize upper-cases the first rune of s.
func Capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || (r == utf8.RuneError && size == 1) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// SnakeCase lower-cases s, inserting an underscore before every upper-case rune after the first.
func SnakeCase(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// TrimQuotes removes one matching pair of surrounding double or single quotes.
func TrimQuotes(s string) string {
	for _, quote := range []string{`"`, `'`} {
		inner, ok := strings.CutPrefix(s, quote)
		if !ok {
			continue
		}
		if inner, ok = strings.CutSuffix(inner, quote); ok {
			return inner
		}
	}
	return s
}

// EnsurePrefix trims s and returns it unchanged when it already starts with prefix, otherwise prefix+s.
func EnsurePrefix(s, prefix string) string {
	s = strings.TrimSpace(s)
	return Ternary(strings.HasPrefix(s, prefix), s, prefix+s)
}

// TrimNonEmpty trims every entry and drops the blank ones, preserving order.
func TrimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// RandomString returns length URL-safe base64 characters drawn from crypto/rand.
func RandomString(length int) string {
	if length <= 0 {
		return ""
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)[:length]
}
