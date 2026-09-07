// Package tagpolicy selects newer image tags using semantic version policies.
package tagpolicy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"go.getarcane.app/updater/types"
)

// Select returns the highest eligible newer tag, or current when none qualifies.
// Equal candidate versions are resolved using the lexically smallest tag.
func Select(current string, tags []string, policy types.Policy) (string, error) {
	pattern, constraint, err := compileInternal(policy)
	if err != nil {
		return "", err
	}
	currentVersion, err := parseInternal(current, pattern)
	if err != nil {
		return "", fmt.Errorf("invalid current tag %q: %w", current, err)
	}
	if pattern == nil && currentVersion.Prerelease() != "" && (constraint == nil || !constraint.Check(currentVersion)) {
		return "", fmt.Errorf("current tag %q needs a tag pattern or an explicit constraint admitting its prerelease", current)
	}
	selected := current
	best := currentVersion
	for _, tag := range tags {
		version, err := parseInternal(tag, pattern)
		if err != nil || !version.GreaterThan(currentVersion) {
			continue
		}
		if constraint != nil {
			if !constraint.Check(version) {
				continue
			}
		} else if version.Prerelease() != "" || version.Major() != currentVersion.Major() || (currentVersion.Major() == 0 && version.Minor() != currentVersion.Minor()) {
			continue
		}
		if version.GreaterThan(best) || (version.Equal(best) && tag < selected) {
			selected, best = tag, version
		}
	}
	return selected, nil
}

// Version extracts and parses a tag's complete semantic version.
func Version(tag string, policy types.Policy) (string, error) {
	pattern, _, err := compileInternal(policy)
	if err != nil {
		return "", err
	}
	version, err := parseInternal(tag, pattern)
	if err != nil {
		return "", err
	}
	return version.String(), nil
}

func compileInternal(policy types.Policy) (*regexp.Regexp, *semver.Constraints, error) {
	var pattern *regexp.Regexp
	if policy.TagPattern != "" {
		var err error
		pattern, err = regexp.Compile(`\A(?:` + policy.TagPattern + `)\z`)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid tag pattern: %w", err)
		}
		count := 0
		for _, name := range pattern.SubexpNames() {
			if name == "version" {
				count++
			}
		}
		if count > 1 {
			return nil, nil, errors.New("tag pattern must have at most one named version capture")
		}
	}
	var constraint *semver.Constraints
	if policy.Constraint != "" {
		var err error
		constraint, err = semver.NewConstraint(policy.Constraint)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid tag constraint: %w", err)
		}
	}
	return pattern, constraint, nil
}

func parseInternal(tag string, pattern *regexp.Regexp) (*semver.Version, error) {
	value := tag
	if pattern != nil {
		matches := pattern.FindStringSubmatch(tag)
		if matches == nil {
			return nil, errors.New("tag does not match tag pattern")
		}
		if index := pattern.SubexpIndex("version"); index >= 0 {
			value = matches[index]
		}
	}
	version, err := semver.StrictNewVersion(strings.TrimPrefix(value, "v"))
	if err != nil {
		return nil, fmt.Errorf("tag must contain a complete semantic version: %w", err)
	}
	return version, nil
}
