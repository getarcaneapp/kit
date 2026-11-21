package semver_test

import (
	"errors"
	"slices"
	"testing"

	"kit.getarcane.app/semver"
)

func TestParseValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		raw           string
		wantString    string
		wantCanonical string
		wantPre       string
		wantMeta      string
		wantMajor     uint64
		wantMinor     uint64
		wantPatch     uint64
		wantStrictErr bool
	}{
		{
			name:          "basic",
			raw:           "1.2.3",
			wantMajor:     1,
			wantMinor:     2,
			wantPatch:     3,
			wantString:    "1.2.3",
			wantCanonical: "v1.2.3",
		},
		{
			name:          "v prefix",
			raw:           "v1.2.3",
			wantMajor:     1,
			wantMinor:     2,
			wantPatch:     3,
			wantString:    "1.2.3",
			wantCanonical: "v1.2.3",
		},
		{
			name:          "zeros",
			raw:           "0.0.0",
			wantString:    "0.0.0",
			wantCanonical: "v0.0.0",
		},
		{
			name:          "major only lenient",
			raw:           "v5",
			wantMajor:     5,
			wantString:    "5.0.0",
			wantCanonical: "v5.0.0",
			wantStrictErr: true,
		},
		{
			name:          "major minor lenient",
			raw:           "5.8",
			wantMajor:     5,
			wantMinor:     8,
			wantString:    "5.8.0",
			wantCanonical: "v5.8.0",
			wantStrictErr: true,
		},
		{
			name:          "prerelease and metadata",
			raw:           "v2.4.0-next.1+sha.abcdef0",
			wantMajor:     2,
			wantMinor:     4,
			wantPatch:     0,
			wantPre:       "next.1",
			wantMeta:      "sha.abcdef0",
			wantString:    "2.4.0-next.1+sha.abcdef0",
			wantCanonical: "v2.4.0-next.1",
		},
		{
			name:          "prerelease with hyphen",
			raw:           "1.0.0-x-y-z.--",
			wantMajor:     1,
			wantPre:       "x-y-z.--",
			wantString:    "1.0.0-x-y-z.--",
			wantCanonical: "v1.0.0-x-y-z.--",
		},
		{
			name:          "prerelease numeric zero",
			raw:           "1.0.0-0",
			wantMajor:     1,
			wantPre:       "0",
			wantString:    "1.0.0-0",
			wantCanonical: "v1.0.0-0",
		},
		{
			name:          "prerelease alphanumeric leading zero",
			raw:           "1.0.0-0a",
			wantMajor:     1,
			wantPre:       "0a",
			wantString:    "1.0.0-0a",
			wantCanonical: "v1.0.0-0a",
		},
		{
			name:          "build metadata leading zero allowed",
			raw:           "1.0.0+001.20130313",
			wantMajor:     1,
			wantMeta:      "001.20130313",
			wantString:    "1.0.0+001.20130313",
			wantCanonical: "v1.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v, err := semver.Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.raw, err)
			}
			if v.Major() != tt.wantMajor || v.Minor() != tt.wantMinor || v.Patch() != tt.wantPatch {
				t.Fatalf("Parse(%q) = %d.%d.%d, want %d.%d.%d", tt.raw, v.Major(), v.Minor(), v.Patch(), tt.wantMajor, tt.wantMinor, tt.wantPatch)
			}
			if v.Prerelease() != tt.wantPre || v.Metadata() != tt.wantMeta || v.Original() != tt.raw {
				t.Fatalf("Parse(%q) fields mismatch: pre=%q meta=%q orig=%q", tt.raw, v.Prerelease(), v.Metadata(), v.Original())
			}
			if v.String() != tt.wantString || v.Canonical() != tt.wantCanonical {
				t.Fatalf("Parse(%q) string=%q canonical=%q, want %q / %q", tt.raw, v.String(), v.Canonical(), tt.wantString, tt.wantCanonical)
			}
			if v.IsPrerelease() != (tt.wantPre != "") || v.IsStable() != (tt.wantPre == "") {
				t.Fatalf("IsPrerelease=%v IsStable=%v mismatch for pre=%q", v.IsPrerelease(), v.IsStable(), tt.wantPre)
			}

			_, strictErr := semver.ParseStrict(tt.raw)
			if tt.wantStrictErr && !errors.Is(strictErr, semver.ErrInvalidVersion) {
				t.Fatalf("ParseStrict(%q) error = %v, want ErrInvalidVersion", tt.raw, strictErr)
			}
			if !tt.wantStrictErr && strictErr != nil {
				t.Fatalf("ParseStrict(%q) unexpected error: %v", tt.raw, strictErr)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()

	invalid := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"only v", "v"},
		{"double v", "vv1.2.3"},
		{"latest", "latest"},
		{"leading zero major", "01.2.3"},
		{"leading zero minor", "1.02.3"},
		{"leading zero patch", "1.2.03"},
		{"four components", "1.2.3.4"},
		{"trailing dot", "1.2."},
		{"empty prerelease", "1.2.3-"},
		{"empty prerelease ident", "1.2.3-alpha..1"},
		{"leading zero numeric prerelease", "1.2.3-01"},
		{"invalid prerelease char", "1.2.3-alpha_1"},
		{"empty build metadata", "1.2.3+"},
		{"empty build ident", "1.2.3+build..1"},
		{"invalid build char", "1.2.3+build_1"},
		{"whitespace", " 1.2.3 "},
		{"uint64 overflow", "99999999999999999999999.0.0"},
	}

	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := semver.Parse(tt.raw); !errors.Is(err, semver.ErrInvalidVersion) {
				t.Fatalf("Parse(%q) error = %v, want ErrInvalidVersion", tt.raw, err)
			}
			if _, err := semver.ParseStrict(tt.raw); !errors.Is(err, semver.ErrInvalidVersion) {
				t.Fatalf("ParseStrict(%q) error = %v, want ErrInvalidVersion", tt.raw, err)
			}
		})
	}
}

func TestVersionCompareOrder(t *testing.T) {
	t.Parallel()

	// Official SemVer 2.0.0 §11 precedence order.
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-beta.999999999999999999999999999999",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0-alpha.1",
		"2.0.0",
		"2.1.0",
		"2.1.1",
	}

	for i := range ordered {
		vi := semver.MustParseStrict(ordered[i])
		viCopy := semver.MustParseStrict(ordered[i])
		if !vi.Equal(viCopy) || vi.LessThan(viCopy) || vi.GreaterThan(viCopy) || !vi.LessThanEqual(viCopy) || !vi.GreaterThanEqual(viCopy) {
			t.Fatalf("reflexive comparison failed for %s", ordered[i])
		}
		for j := i + 1; j < len(ordered); j++ {
			vj := semver.MustParseStrict(ordered[j])
			if !vi.LessThan(vj) || !vj.GreaterThan(vi) || vi.Compare(vj) != -1 || vj.Compare(vi) != 1 {
				t.Fatalf("expected %s < %s", ordered[i], ordered[j])
			}
			if semver.Compare(ordered[i], ordered[j]) != -1 {
				t.Fatalf("Compare(%q, %q) != -1", ordered[i], ordered[j])
			}
		}
	}

	// Build metadata must be ignored for precedence.
	a := semver.MustParseStrict("1.0.0+20130313144700")
	b := semver.MustParseStrict("1.0.0+exp.sha.5114f85")
	if !a.Equal(b) || a.Compare(b) != 0 {
		t.Fatalf("expected build metadata to be ignored in precedence comparison")
	}
}

func TestStringHelpers(t *testing.T) {
	t.Parallel()

	if !semver.IsValid("1.2.3") || !semver.IsValid("v1.2.3") || !semver.IsValid("5.8") || semver.IsValid("latest") {
		t.Fatal("IsValid mismatch")
	}
	if !semver.IsValidStrict("v1.2.3") || semver.IsValidStrict("5.8") {
		t.Fatal("IsValidStrict mismatch")
	}
	if !semver.IsStable("1.2.3") || !semver.IsStable("v1.2.3") || semver.IsStable("1.2.3-rc.1") || semver.IsStable("latest") {
		t.Fatal("IsStable mismatch")
	}
	if got := semver.Major("5.8.2"); got != "v5" {
		t.Fatalf("Major = %q, want v5", got)
	}
	if got := semver.Major("invalid"); got != "" {
		t.Fatalf("Major(invalid) = %q, want empty", got)
	}
	if got := semver.MajorMinor("5.8.2-rc.1"); got != "v5.8" {
		t.Fatalf("MajorMinor = %q, want v5.8", got)
	}
	if got := semver.Prerelease("v1.2.3-next.1+meta"); got != "-next.1" {
		t.Fatalf("Prerelease = %q, want -next.1", got)
	}
	if got := semver.Prerelease("v1.2.3"); got != "" {
		t.Fatalf("Prerelease(stable) = %q, want empty", got)
	}
	if got := semver.Build("v1.2.3-next.1+meta.1"); got != "+meta.1" {
		t.Fatalf("Build = %q, want +meta.1", got)
	}
	if got := semver.Canonical("1.2"); got != "v1.2.0" {
		t.Fatalf("Canonical(1.2) = %q, want v1.2.0", got)
	}
	if got := semver.Canonical("1.2.0+meta"); got != "v1.2.0" {
		t.Fatalf("Canonical(1.2.0+meta) = %q, want v1.2.0", got)
	}
	if got := semver.Max("v1.2.3", "1.3.0"); got != "1.3.0" {
		t.Fatalf("Max = %q, want 1.3.0", got)
	}
	if got := semver.Max("invalid", "1.0.0"); got != "1.0.0" {
		t.Fatalf("Max(invalid, valid) = %q, want 1.0.0", got)
	}
	if got := semver.Max(semver.MustParse("1.2.3"), semver.MustParse("1.3.0")); got.String() != "1.3.0" {
		t.Fatalf("Max(Version) = %q, want 1.3.0", got.String())
	}
	if semver.Compare("invalid", "also-invalid") != 0 || semver.Compare("invalid", "0.0.0") != -1 || semver.Compare("0.0.0", "invalid") != 1 {
		t.Fatal("Compare with invalid strings failed")
	}

	list := []string{"v2.0.0", "1.0.0-alpha", "1.0.0", "v1.0.0-beta", "invalid", "0.9.0"}
	semver.Sort(list)
	want := []string{"invalid", "0.9.0", "1.0.0-alpha", "v1.0.0-beta", "1.0.0", "v2.0.0"}
	if !slices.Equal(list, want) {
		t.Fatalf("Sort = %v, want %v", list, want)
	}

	versions := []semver.Version{semver.MustParse("2.0.0"), semver.MustParse("1.0.0"), semver.MustParse("1.5.0")}
	semver.Sort(versions)
	if versions[0].String() != "1.0.0" || versions[1].String() != "1.5.0" || versions[2].String() != "2.0.0" {
		t.Fatalf("Sort(Version) = %v", versions)
	}

	type release struct {
		tag string
	}
	releases := []release{{tag: "v2.0.0"}, {tag: "v1.1.0"}, {tag: "v1.10.0"}}
	semver.SortBy(releases, func(r release) string { return r.tag })
	if releases[0].tag != "v1.1.0" || releases[1].tag != "v1.10.0" || releases[2].tag != "v2.0.0" {
		t.Fatalf("SortBy = %v", releases)
	}

	c := semver.MustParseConstraint("^1.0.0")
	filtered := semver.Filter(c, []string{"0.9.0", "1.0.0", "1.5.0", "2.0.0", "invalid"})
	if !slices.Equal(filtered, []string{"1.0.0", "1.5.0"}) {
		t.Fatalf("Filter(strings) = %v", filtered)
	}
	filteredV := semver.Filter(c, versions)
	if len(filteredV) != 2 || filteredV[0].String() != "1.0.0" || filteredV[1].String() != "1.5.0" {
		t.Fatalf("Filter(Versions) = %v", filteredV)
	}
}

func TestVersionTextMarshaling(t *testing.T) {
	t.Parallel()

	var v semver.Version
	if err := v.UnmarshalText([]byte("v1.2.3-rc.1+build.4")); err != nil {
		t.Fatalf("UnmarshalText failed: %v", err)
	}
	data, err := v.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText failed: %v", err)
	}
	if string(data) != "1.2.3-rc.1+build.4" {
		t.Fatalf("MarshalText = %q, want 1.2.3-rc.1+build.4", string(data))
	}
}
