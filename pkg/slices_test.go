package kit

import (
	"slices"
	"testing"
)

type namedIDs []int

func TestUnique(t *testing.T) {
	t.Parallel()

	t.Run("preserves first-seen order", func(t *testing.T) {
		t.Parallel()
		got := Unique([]string{"b", "a", "b", "c", "a"})
		if want := []string{"b", "a", "c"}; !slices.Equal(got, want) {
			t.Errorf("Unique = %q, want %q", got, want)
		}
	})

	t.Run("empty returns nil", func(t *testing.T) {
		t.Parallel()
		if got := Unique([]int{}); got != nil {
			t.Errorf("Unique(empty) = %v, want nil", got)
		}
	})

	t.Run("keeps named slice type", func(t *testing.T) {
		t.Parallel()
		got := Unique(namedIDs{3, 3, 1})
		if want := (namedIDs{3, 1}); !slices.Equal(got, want) {
			t.Errorf("Unique = %v, want %v", got, want)
		}
	})

	t.Run("composes with TrimNonEmpty", func(t *testing.T) {
		t.Parallel()
		got := Unique(TrimNonEmpty([]string{" beta ", "alpha", "beta", "Alpha", ""}))
		if want := []string{"beta", "alpha", "Alpha"}; !slices.Equal(got, want) {
			t.Errorf("Unique(TrimNonEmpty) = %q, want %q", got, want)
		}
		if got = Unique(TrimNonEmpty([]string{"", " \t "})); got != nil {
			t.Errorf("Unique(TrimNonEmpty(blank)) = %q, want nil", got)
		}
	})

	t.Run("does not modify input", func(t *testing.T) {
		t.Parallel()
		input := []int{1, 1, 2}
		Unique(input)
		if want := []int{1, 1, 2}; !slices.Equal(input, want) {
			t.Errorf("input modified to %v", input)
		}
	})
}

func TestCollect(t *testing.T) {
	t.Parallel()

	type labels []string
	tests := []struct {
		name  string
		value any
		want  []string
	}{
		{name: "nil", value: nil, want: nil},
		{name: "scalar", value: "one", want: []string{"one"}},
		{name: "integer scalar", value: 7, want: []string{"7"}},
		{name: "any slice", value: []any{"a", 2, true}, want: []string{"a", "2", "true"}},
		{name: "string slice", value: []string{"a", "b"}, want: []string{"a", "b"}},
		{name: "named slice", value: labels{"x"}, want: []string{"x"}},
		{name: "array", value: [2]int{1, 2}, want: []string{"1", "2"}},
		{name: "empty slice", value: []string{}, want: []string{}},
		{name: "bytes are one value", value: []byte("ab"), want: []string{"[97 98]"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Collect(tt.value, ToString)
			if (got == nil) != (tt.want == nil) || !slices.Equal(got, tt.want) {
				t.Errorf("Collect(%v) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}

	t.Run("mapper receives elements", func(t *testing.T) {
		t.Parallel()
		got := Collect([]int{1, 2}, func(v any) int { return v.(int) * 10 })
		if want := []int{10, 20}; !slices.Equal(got, want) {
			t.Errorf("Collect = %v, want %v", got, want)
		}
	})
}
