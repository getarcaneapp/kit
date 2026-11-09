package kit

import (
	"maps"
	"testing"
)

type (
	namedLabels map[string]string
)

func TestAsStringMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value any
		want  map[string]any
		name  string
		ok    bool
	}{
		{name: "nil"},
		{name: "not a map", value: "text"},
		{name: "int keys", value: map[int]string{1: "a"}},
		{name: "string any", value: map[string]any{"a": 1}, want: map[string]any{"a": 1}, ok: true},
		{name: "string string", value: map[string]string{"a": "b"}, want: map[string]any{"a": "b"}, ok: true},
		{name: "named map", value: namedLabels{"a": "b"}, want: map[string]any{"a": "b"}, ok: true},
		{name: "any keys keeps strings", value: map[any]any{"a": 1, 2: "dropped", nil: "dropped"}, want: map[string]any{"a": 1}, ok: true},
		{name: "empty any keys", value: map[any]any{}, want: map[string]any{}, ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := AsStringMap(tt.value)
			if ok != tt.ok {
				t.Fatalf("AsStringMap(%v) ok = %t, want %t", tt.value, ok, tt.ok)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("AsStringMap(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}

	t.Run("returns the same map for map[string]any", func(t *testing.T) {
		t.Parallel()
		in := map[string]any{"a": 1}
		got, _ := AsStringMap(in)
		got["b"] = 2
		if _, ok := in["b"]; !ok {
			t.Error("AsStringMap copied map[string]any instead of returning it")
		}
	})
}
