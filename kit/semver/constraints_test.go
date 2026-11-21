package semver_test

import (
	"errors"
	"testing"

	"kit.getarcane.app/semver"
)

func TestConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint string
		version    string
		want       bool
	}{
		// Exact and basic comparators.
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "v1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{"=1.2.3", "1.2.3", true},
		{"== 1.2.3", "1.2.3", true},
		{"!=1.2.3", "1.2.3", false},
		{"!=1.2.3", "1.2.4", true},
		{">1.2.3", "1.2.4", true},
		{">1.2.3", "1.2.3", false},
		{">=1.2.3", "1.2.3", true},
		{">= 1.2.3", "1.2.2", false},
		{"<2.0.0", "1.9.9", true},
		{"<2.0.0", "2.0.0", false},
		{"<=2.0.0", "2.0.0", true},
		{"<=2.0.0", "2.0.1", false},

		// Partial versions and wildcards.
		{"*", "0.0.0", true},
		{"*", "9.9.9", true},
		{"*", "1.0.0-alpha", false},
		{"*-0", "1.0.0-alpha", true},
		{"x", "1.2.3", true},
		{"X", "1.2.3", true},
		{"4.x", "4.0.0", true},
		{"4.x", "4.9.9", true},
		{"4.x", "3.9.9", false},
		{"4.x", "5.0.0", false},
		{"4.x", "4.1.0-rc.1", false},
		{"4.2.x", "4.2.0", true},
		{"4.2.x", "4.2.9", true},
		{"4.2.x", "4.3.0", false},
		{"4.2.*", "4.2.5", true},
		{"v4.2", "4.2.5", true},
		{"v4.2", "4.3.0", false},
		{"!=4.x", "3.9.9", true},
		{"!=4.x", "4.5.0", false},
		{"!=4.2.x", "4.1.9", true},
		{"!=4.2.x", "4.2.5", false},
		{"!=4.2.x", "4.3.0", true},
		{">4.x", "5.0.0", true},
		{">4.x", "4.9.9", false},
		{"<=4.x", "4.9.9", true},
		{"<=4.x", "5.0.0", false},
		{"<4", "3.9.9", true},
		{"<4", "4.0.0", false},

		// Tilde ranges.
		{"~1.2.3", "1.2.3", true},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{"~1.2.3", "1.2.2", false},
		{"~1.2", "1.2.0", true},
		{"~1.2", "1.2.9", true},
		{"~1.2", "1.3.0", false},
		{"~1", "1.0.0", true},
		{"~1", "1.9.9", true},
		{"~1", "2.0.0", false},
		{"~0.2.3", "0.2.3", true},
		{"~0.2.3", "0.2.9", true},
		{"~0.2.3", "0.3.0", false},
		{"~>1.2.3", "1.2.5", true},
		{"~>1.2.3", "1.3.0", false},

		// Caret ranges.
		{"^1.2.3", "1.2.3", true},
		{"^1.2.3", "1.9.9", true},
		{"^1.2.3", "2.0.0", false},
		{"^1.2.3", "1.2.2", false},
		{"^0.2.3", "0.2.3", true},
		{"^0.2.3", "0.2.9", true},
		{"^0.2.3", "0.3.0", false},
		{"^0.0.3", "0.0.3", true},
		{"^0.0.3", "0.0.4", false},
		{"^0.0.0", "0.0.0", true},
		{"^0.0.0", "0.0.1", false},
		{"^1.2", "1.9.0", true},
		{"^1.2", "2.0.0", false},
		{"^0.2", "0.2.9", true},
		{"^0.2", "0.3.0", false},
		{"^0.0", "0.0.9", true},
		{"^0.0", "0.1.0", false},
		{"^1", "1.9.9", true},
		{"^1", "2.0.0", false},
		{"^0", "0.9.9", true},
		{"^0", "1.0.0", false},

		// Hyphen ranges.
		{"1.2.3 - 2.3.4", "1.2.3", true},
		{"1.2.3 - 2.3.4", "2.3.4", true},
		{"1.2.3 - 2.3.4", "1.2.2", false},
		{"1.2.3 - 2.3.4", "2.3.5", false},
		{"1.2 - 2.3", "1.2.0", true},
		{"1.2 - 2.3", "2.3.9", true},
		{"1.2 - 2.3", "2.4.0", false},
		{"1 - 2", "1.0.0", true},
		{"1 - 2", "2.9.9", true},
		{"1 - 2", "3.0.0", false},

		// AND and OR combinations.
		{">=1.0.0, <2.0.0", "1.5.0", true},
		{">=1.0.0, <2.0.0", "2.0.0", false},
		{">=1.0.0 <2.0.0 || >=3.0.0 <4.0.0", "1.2.0", true},
		{">=1.0.0 <2.0.0 || >=3.0.0 <4.0.0", "2.5.0", false},
		{">=1.0.0 <2.0.0 || >=3.0.0 <4.0.0", "3.1.0", true},

		// Prerelease rules (Masterminds semantics).
		{"3.x", "3.2.0-rc.1", false},
		{">=3.1.9-0 <4.0.0", "3.1.9-rc.1", true},
		{">=3.1.9-0 <4.0.0", "3.2.0-rc.1", true},
		{">=3.1.9-0 <4.0.0", "4.0.0-rc.1", false},
		{">=3.1.9-0 <4.0.0", "4.0.0", false},
		{">=3.1.2-0 <4", "3.1.2-rc.1", true},
		{">=3.1.2-0 <4", "4.0.0-alpha.1", false},
		{"^1.2.3-beta.2", "1.2.3-beta.4", true},
		{"^1.2.3-beta.2", "1.3.0-alpha.1", true},
		{"^1.2.3-beta.2", "2.0.0-alpha.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.constraint+"__"+tt.version, func(t *testing.T) {
			t.Parallel()
			c, err := semver.NewConstraint(tt.constraint)
			if err != nil {
				t.Fatalf("NewConstraint(%q) unexpected error: %v", tt.constraint, err)
			}
			if c.String() != tt.constraint {
				t.Fatalf("String() = %q, want %q", c.String(), tt.constraint)
			}
			v := semver.MustParse(tt.version)
			if got := c.Check(v); got != tt.want {
				t.Fatalf("(%q).Check(%q) = %v, want %v", tt.constraint, tt.version, got, tt.want)
			}
			if got := c.CheckString(tt.version); got != tt.want {
				t.Fatalf("(%q).CheckString(%q) = %v, want %v", tt.constraint, tt.version, got, tt.want)
			}
		})
	}
}

func TestInvalidConstraints(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"",
		"   ",
		"not a range",
		"invalid",
		">=1.0.0 ||",
		"|| >=1.0.0",
		">=",
		"1.2.3.4",
		"1.x.2",
		"x.1.0",
		"01.2.3",
		"1.2.3 -",
		"- 1.2.3",
		"1.2.3 - invalid",
		">=1.2.3-",
		">=1.2.3+",
	}

	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := semver.NewConstraint(raw)
			if !errors.Is(err, semver.ErrInvalidConstraint) {
				t.Fatalf("NewConstraint(%q) error = %v, want ErrInvalidConstraint", raw, err)
			}
		})
	}
}
