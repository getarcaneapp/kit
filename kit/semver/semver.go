// Package semver parses, compares, and evaluates Semantic Versioning 2.0.0
// versions and version constraints on top of golang.org/x/mod/semver.
//
// Unlike golang.org/x/mod/semver, every function in this package accepts
// version strings with or without a leading "v" prefix.
package semver

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	xsemver "golang.org/x/mod/semver"
)

type (
	// Input constrains generic semver helpers to version strings or parsed Version values.
	Input interface {
		~string | Version
	}

	// Version represents a parsed Semantic Version 2.0.0 value.
	Version struct {
		prerelease string
		metadata   string
		original   string
		canonical  string
		major      uint64
		minor      uint64
		patch      uint64
	}
)

var (
	// ErrInvalidVersion reports that a string is not a valid semantic version.
	ErrInvalidVersion = errors.New("invalid semantic version")
	// ErrInvalidConstraint reports that a string is not a valid version constraint.
	ErrInvalidConstraint = errors.New("invalid version constraint")
)

// Parse parses a semantic version string. An optional leading "v" is allowed,
// and missing minor or patch components default to 0 (for example, "1.2" parses
// as 1.2.0 and "v1" parses as 1.0.0).
func Parse[S ~string](raw S) (Version, error) {
	rawStr := string(raw)
	norm := normalize(rawStr)
	if !xsemver.IsValid(norm) {
		return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, rawStr)
	}

	canon := xsemver.Canonical(norm)
	core, _, _ := strings.Cut(strings.TrimPrefix(canon, "v"), "-")
	majorStr, rest, _ := strings.Cut(core, ".")
	minorStr, patchStr, _ := strings.Cut(rest, ".")

	major, err := strconv.ParseUint(majorStr, 10, 64)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %q: %w", ErrInvalidVersion, rawStr, err)
	}
	minor, err := strconv.ParseUint(minorStr, 10, 64)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %q: %w", ErrInvalidVersion, rawStr, err)
	}
	patch, err := strconv.ParseUint(patchStr, 10, 64)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %q: %w", ErrInvalidVersion, rawStr, err)
	}

	return Version{
		prerelease: strings.TrimPrefix(xsemver.Prerelease(norm), "-"),
		metadata:   strings.TrimPrefix(xsemver.Build(norm), "+"),
		original:   rawStr,
		canonical:  canon,
		major:      major,
		minor:      minor,
		patch:      patch,
	}, nil
}

// ParseStrict parses a complete Semantic Version 2.0.0 string requiring all
// three core components (MAJOR.MINOR.PATCH). An optional leading "v" is allowed.
func ParseStrict[S ~string](raw S) (Version, error) {
	v, err := Parse(raw)
	if err != nil {
		return Version{}, err
	}
	core := strings.TrimPrefix(normalize(string(raw)), "v")
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		core = core[:idx]
	}
	if strings.Count(core, ".") != 2 {
		return Version{}, fmt.Errorf("%w: %q must have major.minor.patch", ErrInvalidVersion, string(raw))
	}
	return v, nil
}

// MustParse parses raw with Parse and panics if it is invalid.
func MustParse[S ~string](raw S) Version {
	v, err := Parse(raw)
	if err != nil {
		panic(err)
	}
	return v
}

// MustParseStrict parses raw with ParseStrict and panics if it is invalid.
func MustParseStrict[S ~string](raw S) Version {
	v, err := ParseStrict(raw)
	if err != nil {
		panic(err)
	}
	return v
}

// Major returns the major version number.
func (v Version) Major() uint64 {
	return v.major
}

// Minor returns the minor version number.
func (v Version) Minor() uint64 {
	return v.minor
}

// Patch returns the patch version number.
func (v Version) Patch() uint64 {
	return v.patch
}

// Prerelease returns the prerelease identifier without a leading "-".
func (v Version) Prerelease() string {
	return v.prerelease
}

// Metadata returns the build metadata without a leading "+".
func (v Version) Metadata() string {
	return v.metadata
}

// Original returns the original input string used to construct v, or v.String()
// when v was not parsed from a string.
func (v Version) Original() string {
	if v.original != "" {
		return v.original
	}
	return v.String()
}

// IsPrerelease reports whether v has a prerelease identifier.
func (v Version) IsPrerelease() bool {
	return v.prerelease != ""
}

// IsStable reports whether v has no prerelease identifier.
func (v Version) IsStable() bool {
	return v.prerelease == ""
}

// String returns the canonical Semantic Version 2.0.0 string without a leading "v".
func (v Version) String() string {
	base := strings.TrimPrefix(v.Canonical(), "v")
	if v.metadata != "" {
		return base + "+" + v.metadata
	}
	return base
}

// Canonical returns the canonical "vMAJOR.MINOR.PATCH[-prerelease]" string
// without build metadata.
func (v Version) Canonical() string {
	if v.canonical != "" {
		return v.canonical
	}
	s := "v" + strconv.FormatUint(v.major, 10) + "." + strconv.FormatUint(v.minor, 10) + "." + strconv.FormatUint(v.patch, 10)
	if v.prerelease != "" {
		s += "-" + v.prerelease
	}
	return s
}

// MajorMinor returns the "vMAJOR.MINOR" prefix of v.
func (v Version) MajorMinor() string {
	return "v" + strconv.FormatUint(v.major, 10) + "." + strconv.FormatUint(v.minor, 10)
}

// Compare compares v and other according to Semantic Versioning 2.0.0 precedence
// rules, ignoring build metadata. It returns -1 if v < other, 0 if v == other,
// and +1 if v > other.
func (v Version) Compare(other Version) int {
	return xsemver.Compare(v.Canonical(), other.Canonical())
}

// Equal reports whether v and other have equal SemVer precedence.
func (v Version) Equal(other Version) bool {
	return v.Compare(other) == 0
}

// LessThan reports whether v has lower precedence than other.
func (v Version) LessThan(other Version) bool {
	return v.Compare(other) < 0
}

// LessThanEqual reports whether v has lower or equal precedence to other.
func (v Version) LessThanEqual(other Version) bool {
	return v.Compare(other) <= 0
}

// GreaterThan reports whether v has higher precedence than other.
func (v Version) GreaterThan(other Version) bool {
	return v.Compare(other) > 0
}

// GreaterThanEqual reports whether v has higher or equal precedence to other.
func (v Version) GreaterThanEqual(other Version) bool {
	return v.Compare(other) >= 0
}

// MarshalText encodes v as its canonical SemVer string.
func (v Version) MarshalText() ([]byte, error) {
	return []byte(v.String()), nil
}

// UnmarshalText decodes a semantic version string into v.
func (v *Version) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// IsValid reports whether v is a valid semantic version (with an optional
// leading "v" and optional minor/patch components).
func IsValid[T Input](v T) bool {
	return xsemver.IsValid(normalize(v))
}

// IsValidStrict reports whether v is a valid complete MAJOR.MINOR.PATCH
// semantic version (with an optional leading "v").
func IsValidStrict[S ~string](v S) bool {
	_, err := ParseStrict(v)
	return err == nil
}

// IsStable reports whether v is a valid semantic version with no prerelease.
func IsStable[T Input](v T) bool {
	norm := normalize(v)
	return xsemver.IsValid(norm) && xsemver.Prerelease(norm) == ""
}

// Compare compares two versions (strings or parsed Version values) according to
// semantic version precedence. Invalid version strings compare less than valid
// ones, and two invalid strings compare equal.
func Compare[T Input](a, b T) int {
	return xsemver.Compare(normalize(a), normalize(b))
}

// Major returns the "vMAJOR" prefix of v, or "" if v is not valid.
func Major[T Input](v T) string {
	return xsemver.Major(normalize(v))
}

// MajorMinor returns the "vMAJOR.MINOR" prefix of v, or "" if v is not valid.
func MajorMinor[T Input](v T) string {
	return xsemver.MajorMinor(normalize(v))
}

// Prerelease returns the prerelease suffix of v including its leading "-", or
// "" if v is invalid or has no prerelease.
func Prerelease[T Input](v T) string {
	return xsemver.Prerelease(normalize(v))
}

// Build returns the build metadata suffix of v including its leading "+", or ""
// if v is invalid or has no build metadata.
func Build[T Input](v T) string {
	return xsemver.Build(normalize(v))
}

// Canonical returns the canonical "vMAJOR.MINOR.PATCH[-prerelease]" form of v
// without build metadata, or "" if v is not valid.
func Canonical[T Input](v T) string {
	return xsemver.Canonical(normalize(v))
}

// Max returns whichever of a and b has higher semantic version precedence.
func Max[T Input](a, b T) T {
	if Compare(a, b) < 0 {
		return b
	}
	return a
}

// Sort sorts a slice of version strings or Version values in ascending semantic
// version order.
func Sort[S ~[]E, E Input](list S) {
	slices.SortFunc(list, func(a, b E) int {
		if c := Compare(a, b); c != 0 {
			return c
		}
		return cmp.Compare(normalize(a), normalize(b))
	})
}

// SortBy sorts items in ascending semantic version order using key to extract
// each element's version string or Version value.
func SortBy[S ~[]E, E any, K Input](items S, key func(E) K) {
	slices.SortStableFunc(items, func(a, b E) int {
		return Compare(key(a), key(b))
	})
}

func normalize[T Input](v T) string {
	if parsed, ok := any(v).(Version); ok {
		return "v" + parsed.String()
	}
	s := fmt.Sprint(v)
	if s == "" || s[0] == 'v' {
		return s
	}
	return "v" + s
}
