package contextsource

import (
	"path"
	"strings"

	"go.getarcane.app/builds/types"
	"go.getarcane.app/kit/pkg/utils/giturl"
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
	if !giturl.IsSupportedRepositoryURL(repositoryURL) {
		return nil, false, nil
	}

	source := &GitBuildContextSource{
		Raw:           trimmed,
		RepositoryURL: strings.TrimRight(repositoryURL, "/"),
	}

	if !hasFragment {
		return source, true, nil
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
	source.Ref = ref

	if !hasSubdir {
		return source, true, nil
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

	source.Subdir = cleanSubdir
	return source, true, nil
}

func NormalizeGitBuildContextSourceForMatch(raw string) string {
	source, ok, err := ParseGitBuildContextSource(raw)
	if err != nil || !ok || source == nil {
		return ""
	}
	return giturl.NormalizeForMatch(source.RepositoryURL)
}

func IsPotentialRemoteBuildContextSource(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}

	return giturl.IsSupportedRepositoryURL(trimmed)
}
