package semver

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	xsemver "golang.org/x/mod/semver"
)

type (
	// Constraints represents a parsed semantic version constraint expression
	// supporting comparators (=, ==, !=, <, <=, >, >=), wildcards (*, x, X),
	// tilde (~, ~>), caret (^), hyphen ranges (A - B), AND (space/comma), and
	// OR (||) groups.
	Constraints struct {
		original string
		groups   [][]comparator
	}

	comparator struct {
		ver Version
		op  compOp
	}

	partialVersion struct {
		ver       Version
		majorWild bool
		minorWild bool
		patchWild bool
	}

	compOp uint8
)

const (
	opEq compOp = iota
	opNotEq
	opLT
	opLTE
	opGT
	opGTE
	opAny
	opNever
	opNotMajor
	opNotMinor
)

// NewConstraint parses a semantic version constraint expression.
func NewConstraint[S ~string](raw S) (*Constraints, error) {
	rawStr := string(raw)
	trimmed := strings.TrimSpace(rawStr)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty constraint", ErrInvalidConstraint)
	}

	operators := []string{"==", "!=", ">=", "<=", "~>", "=", ">", "<", "~", "^"}
	var groups [][]comparator

	for part := range strings.SplitSeq(trimmed, "||") {
		tokens := strings.FieldsFunc(part, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		})
		if len(tokens) == 0 {
			return nil, fmt.Errorf("%w: empty constraint group", ErrInvalidConstraint)
		}

		var comps []comparator
		for i := 0; i < len(tokens); i++ {
			if i+2 < len(tokens) && tokens[i+1] == "-" {
				low, err := parsePartialVersion(tokens[i])
				if err != nil {
					return nil, fmt.Errorf("%w: invalid hyphen range lower bound %q: %w", ErrInvalidConstraint, tokens[i], err)
				}
				high, err := parsePartialVersion(tokens[i+2])
				if err != nil {
					return nil, fmt.Errorf("%w: invalid hyphen range upper bound %q: %w", ErrInvalidConstraint, tokens[i+2], err)
				}
				if low.majorWild {
					comps = append(comps, comparator{op: opAny, ver: low.ver})
				} else {
					comps = append(comps, comparator{op: opGTE, ver: low.ver})
				}
				comps = append(comps, expandClause("<=", high)...)
				i += 2
				continue
			}

			tok := tokens[i]
			if slices.Contains(operators, tok) {
				if i+1 >= len(tokens) {
					return nil, fmt.Errorf("%w: missing version after operator %q", ErrInvalidConstraint, tok)
				}
				i++
				tok += tokens[i]
			}

			var op string
			rest := tok
			for _, prefix := range operators {
				if after, ok := strings.CutPrefix(tok, prefix); ok {
					op, rest = prefix, after
					break
				}
			}

			pv, err := parsePartialVersion(rest)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid constraint %q: %w", ErrInvalidConstraint, tok, err)
			}
			comps = append(comps, expandClause(op, pv)...)
		}
		groups = append(groups, comps)
	}

	return &Constraints{
		original: rawStr,
		groups:   groups,
	}, nil
}

// MustParseConstraint parses raw with NewConstraint and panics on error.
func MustParseConstraint[S ~string](raw S) *Constraints {
	c, err := NewConstraint(raw)
	if err != nil {
		panic(err)
	}
	return c
}

// String returns the original constraint expression.
func (c *Constraints) String() string {
	return c.original
}

// Check reports whether v satisfies c.
//
// Prerelease versions are only matched by an OR-group when at least one
// comparator in that group specifies a prerelease identifier.
func (c *Constraints) Check(v Version) bool {
	for _, group := range c.groups {
		if v.prerelease != "" && !slices.ContainsFunc(group, func(comp comparator) bool {
			return comp.ver.prerelease != ""
		}) {
			continue
		}

		matched := true
		for _, comp := range group {
			var ok bool
			switch comp.op {
			case opAny:
				ok = true
			case opNever:
				ok = false
			case opEq:
				ok = v.Compare(comp.ver) == 0
			case opNotEq:
				ok = v.Compare(comp.ver) != 0
			case opLT:
				sameCore := v.prerelease != "" && comp.ver.prerelease == "" &&
					v.major == comp.ver.major && v.minor == comp.ver.minor && v.patch == comp.ver.patch
				ok = !sameCore && v.Compare(comp.ver) < 0
			case opLTE:
				ok = v.Compare(comp.ver) <= 0
			case opGT:
				ok = v.Compare(comp.ver) > 0
			case opGTE:
				ok = v.Compare(comp.ver) >= 0
			case opNotMajor:
				ok = v.major != comp.ver.major
			case opNotMinor:
				ok = v.major != comp.ver.major || v.minor != comp.ver.minor
			}
			if !ok {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// CheckString parses raw with Parse and reports whether it satisfies c.
func (c *Constraints) CheckString(raw string) bool {
	v, err := Parse(raw)
	return err == nil && c.Check(v)
}

// Filter returns a new slice containing the elements of items that satisfy c.
func Filter[S ~[]E, E Input](c *Constraints, items S) S {
	out := make(S, 0, len(items))
	for _, item := range items {
		if parsed, ok := any(item).(Version); ok {
			if c.Check(parsed) {
				out = append(out, item)
			}
		} else if c.CheckString(fmt.Sprint(item)) {
			out = append(out, item)
		}
	}
	return out
}

func expandClause(op string, pv partialVersion) []comparator {
	ver := pv.ver
	if pv.majorWild {
		if op == "!=" || op == ">" || op == "<" {
			return []comparator{{op: opNever, ver: ver}}
		}
		return []comparator{{op: opAny, ver: ver}}
	}

	bump := func(level int) (Version, bool) {
		switch level {
		case 2:
			if ver.patch < math.MaxUint64 {
				return Version{major: ver.major, minor: ver.minor, patch: ver.patch + 1}, true
			}
			fallthrough
		case 1:
			if ver.minor < math.MaxUint64 {
				return Version{major: ver.major, minor: ver.minor + 1}, true
			}
			fallthrough
		default:
			if ver.major < math.MaxUint64 {
				return Version{major: ver.major + 1}, true
			}
			return Version{}, false
		}
	}
	rangeTo := func(level int) []comparator {
		comps := []comparator{{op: opGTE, ver: ver}}
		if upper, ok := bump(level); ok {
			comps = append(comps, comparator{op: opLT, ver: upper})
		}
		return comps
	}

	wildLevel := 1
	if pv.minorWild {
		wildLevel = 0
	}
	hasWild := pv.minorWild || pv.patchWild

	switch op {
	case "", "=", "==":
		if !hasWild {
			return []comparator{{op: opEq, ver: ver}}
		}
		return rangeTo(wildLevel)
	case "!=":
		if pv.minorWild {
			return []comparator{{op: opNotMajor, ver: ver}}
		}
		if pv.patchWild {
			return []comparator{{op: opNotMinor, ver: ver}}
		}
		return []comparator{{op: opNotEq, ver: ver}}
	case "~", "~>":
		return rangeTo(wildLevel)
	case "^":
		level := 2
		if ver.major > 0 || pv.minorWild {
			level = 0
		} else if ver.minor > 0 || pv.patchWild {
			level = 1
		}
		return rangeTo(level)
	case ">":
		if !hasWild {
			return []comparator{{op: opGT, ver: ver}}
		}
		if upper, ok := bump(wildLevel); ok {
			return []comparator{{op: opGTE, ver: upper}}
		}
		return []comparator{{op: opNever, ver: ver}}
	case ">=":
		return []comparator{{op: opGTE, ver: ver}}
	case "<":
		return []comparator{{op: opLT, ver: ver}}
	case "<=":
		if !hasWild {
			return []comparator{{op: opLTE, ver: ver}}
		}
		if upper, ok := bump(wildLevel); ok {
			return []comparator{{op: opLT, ver: upper}}
		}
		return []comparator{{op: opAny, ver: ver}}
	default:
		return []comparator{{op: opNever, ver: ver}}
	}
}

func parsePartialVersion(raw string) (partialVersion, error) {
	s := strings.TrimPrefix(raw, "v")
	if s == "" {
		return partialVersion{}, errors.New("empty version")
	}

	if coreAndPre, meta, hasMeta := strings.Cut(s, "+"); hasMeta {
		if !xsemver.IsValid("v0.0.0+" + meta) {
			return partialVersion{}, errors.New("invalid build metadata")
		}
		s = coreAndPre
	}

	var prerelease string
	if core, pre, hasPre := strings.Cut(s, "-"); hasPre {
		if !xsemver.IsValid("v0.0.0-" + pre) {
			return partialVersion{}, errors.New("invalid prerelease")
		}
		prerelease = pre
		s = core
	}

	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return partialVersion{}, errors.New("too many version components")
	}

	isWild := func(p string) bool { return p == "*" || p == "x" || p == "X" }
	parsePart := func(p string) (uint64, error) {
		if !xsemver.IsValid("v" + p) {
			return 0, ErrInvalidVersion
		}
		return strconv.ParseUint(p, 10, 64)
	}

	pv := partialVersion{ver: Version{prerelease: prerelease}}
	if isWild(parts[0]) {
		pv.majorWild, pv.minorWild, pv.patchWild = true, true, true
		if slices.ContainsFunc(parts[1:], func(p string) bool { return !isWild(p) }) {
			return partialVersion{}, errors.New("wildcard major cannot have concrete minor or patch")
		}
		return pv, nil
	}

	major, err := parsePart(parts[0])
	if err != nil {
		return partialVersion{}, err
	}
	pv.ver.major = major

	if len(parts) < 2 || isWild(parts[1]) {
		pv.minorWild, pv.patchWild = true, true
		if len(parts) == 3 && !isWild(parts[2]) {
			return partialVersion{}, errors.New("wildcard minor cannot have concrete patch")
		}
		return pv, nil
	}

	minor, err := parsePart(parts[1])
	if err != nil {
		return partialVersion{}, err
	}
	pv.ver.minor = minor

	if len(parts) < 3 || isWild(parts[2]) {
		pv.patchWild = true
		return pv, nil
	}

	patch, err := parsePart(parts[2])
	if err != nil {
		return partialVersion{}, err
	}
	pv.ver.patch = patch
	return pv, nil
}
