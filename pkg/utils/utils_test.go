package utils

import (
	"slices"
	"testing"
)

func TestNormalizeSet(t *testing.T) {
	t.Parallel()

	input := []string{" b ", "a", "", "b", "  ", "a"}
	want := []string{"a", "b"}
	if got := NormalizeSet(input); !slices.Equal(got, want) {
		t.Errorf("NormalizeSet(%q) = %q, want %q", input, got, want)
	}

	if got := NormalizeSet(nil); len(got) != 0 {
		t.Errorf("NormalizeSet(nil) = %q, want empty", got)
	}
}

func TestCountNonEmpty(t *testing.T) {
	t.Parallel()

	if got := CountNonEmpty([]string{"a", " ", "", "b"}); got != 2 {
		t.Errorf("CountNonEmpty = %d, want 2", got)
	}
	if got := CountNonEmpty(nil); got != 0 {
		t.Errorf("CountNonEmpty(nil) = %d, want 0", got)
	}
}

func TestHasNonEmpty(t *testing.T) {
	t.Parallel()

	if !HasNonEmpty([]string{"", "x"}) {
		t.Error("HasNonEmpty = false, want true")
	}
	if HasNonEmpty([]string{"", "  "}) {
		t.Error("HasNonEmpty = true, want false")
	}
}
