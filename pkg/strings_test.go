package kit

import (
	"slices"
	"strings"
	"testing"
)

func TestToString(t *testing.T) {
	t.Parallel()

	var builder strings.Builder
	builder.WriteString("  from stringer  ")

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nil", value: nil, want: ""},
		{name: "string", value: " \tvalue\n", want: "value"},
		{name: "unicode whitespace", value: "\u2003value\u2003", want: "value"},
		{name: "stringer", value: &builder, want: "from stringer"},
		{name: "integer", value: 42, want: "42"},
		{name: "boolean", value: false, want: "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ToString(tt.value); got != tt.want {
				t.Errorf("ToString(%v) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestCapitalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, input, want string
	}{
		{name: "empty", input: "", want: ""},
		{name: "lowercase", input: "hello world", want: "Hello world"},
		{name: "already upper", input: "Hello", want: "Hello"},
		{name: "digit", input: "123abc", want: "123abc"},
		{name: "multibyte", input: "élan", want: "Élan"},
		{name: "invalid utf8", input: "\xffabc", want: "\xffabc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Capitalize(tt.input); got != tt.want {
				t.Errorf("Capitalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSnakeCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, input, want string
	}{
		{name: "empty", input: "", want: ""},
		{name: "simple", input: "camelCase", want: "camel_case"},
		{name: "multiple words", input: "thisIsALongerString", want: "this_is_a_longer_string"},
		{name: "leading upper", input: "PascalCase", want: "pascal_case"},
		{name: "already snake", input: "already_snake", want: "already_snake"},
		{name: "unicode upper", input: "größeÄnderung", want: "größe_änderung"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SnakeCase(tt.input); got != tt.want {
				t.Errorf("SnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTrimQuotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, input, want string
	}{
		{name: "empty", input: "", want: ""},
		{name: "no quotes", input: "hello", want: "hello"},
		{name: "double quotes", input: `"hello"`, want: "hello"},
		{name: "single quotes", input: "'hello'", want: "hello"},
		{name: "only leading", input: `"hello`, want: `"hello`},
		{name: "only trailing", input: "hello'", want: "hello'"},
		{name: "mismatched", input: `'hello"`, want: `'hello"`},
		{name: "lone quote", input: `"`, want: `"`},
		{name: "empty quoted", input: `""`, want: ""},
		{name: "nested left alone", input: `"'hello'"`, want: "'hello'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := TrimQuotes(tt.input); got != tt.want {
				t.Errorf("TrimQuotes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEnsurePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, input, prefix, want string
	}{
		{name: "adds", input: "1.2.3", prefix: "v", want: "v1.2.3"},
		{name: "keeps", input: "v1.2.3", prefix: "v", want: "v1.2.3"},
		{name: "empty input", input: "", prefix: "v", want: "v"},
		{name: "trims", input: "  v1.2.3\n", prefix: "v", want: "v1.2.3"},
		{name: "trims then adds", input: " 1.2.3 ", prefix: "v", want: "v1.2.3"},
		{name: "empty prefix", input: "abc", prefix: "", want: "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := EnsurePrefix(tt.input, tt.prefix); got != tt.want {
				t.Errorf("EnsurePrefix(%q, %q) = %q, want %q", tt.input, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestTrimNonEmpty(t *testing.T) {
	t.Parallel()

	got := TrimNonEmpty([]string{" a ", "", "\t", "b", "a"})
	want := []string{"a", "b", "a"}
	if !slices.Equal(got, want) {
		t.Errorf("TrimNonEmpty = %q, want %q", got, want)
	}
	if got = TrimNonEmpty(nil); len(got) != 0 {
		t.Errorf("TrimNonEmpty(nil) = %q, want empty", got)
	}
}

func TestRandomString(t *testing.T) {
	t.Parallel()

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

	for _, length := range []int{1, 16, 32, 128} {
		got := RandomString(length)
		if len(got) != length {
			t.Errorf("RandomString(%d) length = %d", length, len(got))
		}
		if strings.Trim(got, alphabet) != "" {
			t.Errorf("RandomString(%d) = %q contains characters outside the URL-safe alphabet", length, got)
		}
	}

	if got := RandomString(0); got != "" {
		t.Errorf("RandomString(0) = %q, want empty", got)
	}
	if got := RandomString(-1); got != "" {
		t.Errorf("RandomString(-1) = %q, want empty", got)
	}
	first := RandomString(32)
	second := RandomString(32)
	if first == second {
		t.Error("consecutive RandomString calls returned the same value")
	}
}
