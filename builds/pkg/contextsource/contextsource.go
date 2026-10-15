package contextsource

import (
	"path"
	"strings"

	"go.getarcane.app/kit/pkg/git"

	"go.getarcane.app/builds/types"
)

type GitBuildContextSource struct {
	Raw           string
	RepositoryURL string
	Ref           string
	Subdir        string
}

func ParseGitBuildContextSource(raw string) (*GitBuildContextSource, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false, nil
	}

	repositoryURL, fragment, hasFragment := strings.Cut(trimmed, "#")
	repositoryURL = strings.TrimSpace(repositoryURL)
	if !git.IsSupportedRepositoryURL(repositoryURL) {
		return nil, false, nil
	}

	newSource := func(ref, subdir string) *GitBuildContextSource {
		return &GitBuildContextSource{
			Raw:           trimmed,
			RepositoryURL: strings.TrimRight(repositoryURL, "/"),
			Ref:           ref,
			Subdir:        subdir,
		}
	}

	if !hasFragment {
		return newSource("", ""), true, nil
	}

	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return nil, true, &types.GitBuildContextFragmentRequiredError{}
	}

	ref, subdir, hasSubdir := strings.Cut(fragment, ":")
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, true, &types.GitBuildContextRefRequiredError{}
	}

	if !hasSubdir {
		return newSource(ref, ""), true, nil
	}

	subdir = strings.TrimSpace(subdir)
	if subdir == "" {
		return nil, true, &types.GitBuildContextSubdirRequiredError{}
	}
	if strings.HasPrefix(subdir, "/") {
		return nil, true, &types.GitBuildContextSubdirRelativeError{}
	}

	cleanSubdir := path.Clean(subdir)
	if cleanSubdir == "." || cleanSubdir == ".." || strings.HasPrefix(cleanSubdir, "../") {
		return nil, true, &types.GitBuildContextSubdirEscapesRepositoryError{}
	}

	return newSource(ref, cleanSubdir), true, nil
}

func NormalizeGitBuildContextSourceForMatch(raw string) string {
	source, ok, err := ParseGitBuildContextSource(raw)
	if err != nil || !ok {
		return ""
	}
	return git.NormalizeForMatch(source.RepositoryURL)
}

func IsPotentialRemoteBuildContextSource(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}

	return git.IsSupportedRepositoryURL(trimmed)
}
