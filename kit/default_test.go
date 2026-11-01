package kit

import (
	"errors"
	"strconv"
	"testing"
)

func TestParseOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  int
	}{
		{name: "valid", value: "42", want: 42},
		{name: "valid zero", value: "0", want: 0},
		{name: "invalid", value: "forty-two", want: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseOrDefault(tt.value, 7, strconv.Atoi); got != tt.want {
				t.Errorf("ParseOrDefault(%q, 7, strconv.Atoi) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}

	t.Run("empty skips parser", func(t *testing.T) {
		t.Parallel()
		called := false
		parse := func(string) (int, error) {
			called = true
			return 42, nil
		}
		if got := ParseOrDefault("", 7, parse); got != 7 {
			t.Errorf("ParseOrDefault(empty) = %d, want 7", got)
		}
		if called {
			t.Error("ParseOrDefault called parser for empty input")
		}
	})

	t.Run("passes input unchanged", func(t *testing.T) {
		t.Parallel()
		parse := func(value string) (string, error) {
			return value, nil
		}
		if got := ParseOrDefault("  value  ", "fallback", parse); got != "  value  " {
			t.Errorf("ParseOrDefault = %q, want unmodified input", got)
		}
	})

	t.Run("parser error returns default", func(t *testing.T) {
		t.Parallel()
		parse := func(string) (string, error) {
			return "partial", errors.New("parse failed")
		}
		if got := ParseOrDefault("value", "fallback", parse); got != "fallback" {
			t.Errorf("ParseOrDefault = %q, want fallback", got)
		}
	})
}

func TestAs(t *testing.T) {
	t.Parallel()

	m := map[string]any{"token_type": "bearer", "expires": 300, "nothing": nil}

	if got := As(m["token_type"], "Bearer"); got != "bearer" {
		t.Errorf("As string = %q, want bearer", got)
	}
	if got := As(m["missing"], "Bearer"); got != "Bearer" {
		t.Errorf("As missing = %q, want fallback", got)
	}
	if got := As(m["expires"], "unset"); got != "unset" {
		t.Errorf("As wrong type = %q, want fallback", got)
	}
	if got := As(m["expires"], 0); got != 300 {
		t.Errorf("As int = %d, want 300", got)
	}
	if got := As(m["nothing"], "fallback"); got != "fallback" {
		t.Errorf("As nil = %q, want fallback", got)
	}
	if got := As[any](m["nothing"], "fallback"); got != "fallback" {
		t.Errorf("As nil as any = %v, want fallback", got)
	}
}
