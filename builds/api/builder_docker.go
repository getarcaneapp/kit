package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/containerd/platforms"
	"github.com/docker/cli/cli/config"
	"github.com/docker/go-units"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/go-archive"
	dockerbuild "github.com/moby/moby/api/types/build"
	dockercontainer "github.com/moby/moby/api/types/container"
	dockerregistry "github.com/moby/moby/api/types/registry"
	dockerclient "github.com/moby/moby/client"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	kit "go.getarcane.app/kit/pkg"

	dockerutils "go.getarcane.app/builds/pkg/docker"
	"go.getarcane.app/builds/types"
)

type dockerBuildInput struct {
	buildFilesystemInput

	platform    string
	buildArgs   map[string]*string
	labels      map[string]string
	cacheFrom   []string
	noCache     bool
	pullParent  bool
	networkMode string
	isolation   string
	shmSize     int64
	ulimits     []*dockercontainer.Ulimit
	extraHosts  []string
}

type buildFilesystemInput struct {
	contextDir           string
	fullDockerfilePath   string
	relDockerfile        string
	dockerfileOutsideCtx bool
	dockerfileInline     string
}

func parseUlimits(values map[string]string) ([]*dockercontainer.Ulimit, error) {
	if len(values) == 0 {
		return nil, nil
	}

	out := make([]*dockercontainer.Ulimit, 0, len(values))
	for _, name := range slices.Sorted(maps.Keys(values)) {
		spec := strings.TrimSpace(name)
		raw := strings.TrimSpace(values[name])
		if spec == "" || raw == "" {
			continue
		}

		ulimit, err := units.ParseUlimit(spec + "=" + raw)
		if err != nil {
			return nil, fmt.Errorf("invalid ulimit %q: %w", spec+"="+raw, err)
		}
		out = append(out, ulimit)
	}

	if len(out) == 0 {
		return nil, nil
	}

	return out, nil
}

func prepareBuildFilesystemInput(req types.BuildRequest) (buildFilesystemInput, error) {
	contextDir := filepath.Clean(req.ContextDir)
	if contextDir == "" {
		return buildFilesystemInput{}, &types.BuildContextDirRequiredError{}
	}

	if strings.TrimSpace(req.DockerfileInline) != "" {
		return buildFilesystemInput{
			contextDir:           contextDir,
			relDockerfile:        ".arcane.inline.Dockerfile",
			dockerfileOutsideCtx: true,
			dockerfileInline:     req.DockerfileInline,
		}, nil
	}

	dockerfilePath := cmp.Or(strings.TrimSpace(req.Dockerfile), "Dockerfile")
	fullDockerfilePath := kit.Ternary(filepath.IsAbs(dockerfilePath), dockerfilePath, filepath.Join(contextDir, dockerfilePath))

	relDockerfile, relErr := filepath.Rel(contextDir, fullDockerfilePath)
	dockerfileOutsideCtx, err := func() (bool, error) {
		if relErr != nil || !filepath.IsLocal(relDockerfile) {
			return true, nil
		}
		return dockerfileExcludedByDockerignore(contextDir, relDockerfile)
	}()
	if err != nil {
		return buildFilesystemInput{}, err
	}

	return buildFilesystemInput{
		contextDir:           contextDir,
		fullDockerfilePath:   fullDockerfilePath,
		relDockerfile:        kit.Ternary(dockerfileOutsideCtx, filepath.Base(fullDockerfilePath), filepath.ToSlash(relDockerfile)),
		dockerfileOutsideCtx: dockerfileOutsideCtx,
	}, nil
}

func prepareDockerBuildInput(req types.BuildRequest) (dockerBuildInput, bool, error) {
	fsInput, err := prepareBuildFilesystemInput(req)
	if err != nil {
		return dockerBuildInput{}, false, err
	}

	if len(req.Platforms) > 1 {
		return dockerBuildInput{}, true, &types.DockerBuildMultiPlatformUnsupportedError{}
	}

	ulimits, err := parseUlimits(req.Ulimits)
	if err != nil {
		return dockerBuildInput{}, false, err
	}

	buildArgs := map[string]*string{}
	for key, val := range req.BuildArgs {
		buildArgs[key] = new(val)
	}

	labels := map[string]string{}
	for key, val := range req.Labels {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		labels[k] = val
	}

	cacheFrom := kit.TrimNonEmpty(req.CacheFrom)
	extraHosts := kit.TrimNonEmpty(req.ExtraHosts)

	return dockerBuildInput{
		buildFilesystemInput: fsInput,
		platform: func() string {
			if len(req.Platforms) == 0 {
				return ""
			}
			return strings.TrimSpace(req.Platforms[0])
		}(),
		buildArgs:   buildArgs,
		labels:      kit.Ternary(len(labels) == 0, nil, labels),
		cacheFrom:   kit.Ternary(len(cacheFrom) == 0, nil, cacheFrom),
		noCache:     req.NoCache,
		pullParent:  req.Pull,
		networkMode: strings.TrimSpace(req.Network),
		isolation:   strings.TrimSpace(req.Isolation),
		shmSize:     req.ShmSize,
		ulimits:     ulimits,
		extraHosts:  kit.Ternary(len(extraHosts) == 0, nil, extraHosts),
	}, false, nil
}

func copyFileWithMode(src, dst string, mode os.FileMode) (copyErr error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); copyErr == nil && closeErr != nil {
			copyErr = closeErr
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return nil
}

func prepareBuildContext(input buildFilesystemInput) (string, string, func(), error) {
	if input.dockerfileInline == "" && !input.dockerfileOutsideCtx {
		return input.contextDir, input.relDockerfile, func() {}, nil
	}

	stagingDir, err := os.MkdirTemp("", "arcane-docker-build-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create temporary build directory: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(stagingDir)
	}

	err = filepath.WalkDir(input.contextDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, relErr := filepath.Rel(input.contextDir, path)
		if relErr != nil {
			return relErr
		}
		if relPath == "." {
			return nil
		}

		targetPath := filepath.Join(stagingDir, relPath)
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}

		if d.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, readlinkErr := os.Readlink(path)
			if readlinkErr != nil {
				return readlinkErr
			}
			return os.Symlink(linkTarget, targetPath) //nolint:gosec // build context staging intentionally preserves symlinks from the user-selected source tree.
		}

		mkdirErr := os.MkdirAll(filepath.Dir(targetPath), 0o755)
		if mkdirErr != nil {
			return mkdirErr
		}

		return copyFileWithMode(path, targetPath, info.Mode())
	})
	if err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("failed to stage build context: %w", err)
	}

	if input.dockerfileInline != "" {
		stagedDockerfilePath := filepath.Join(stagingDir, filepath.FromSlash(input.relDockerfile))
		err = os.MkdirAll(filepath.Dir(stagedDockerfilePath), 0o755)
		if err != nil {
			cleanup()
			return "", "", nil, fmt.Errorf("failed to create inline Dockerfile path: %w", err)
		}
		err = os.WriteFile(stagedDockerfilePath, []byte(input.dockerfileInline), 0o600)
		if err != nil {
			cleanup()
			return "", "", nil, fmt.Errorf("failed to stage inline Dockerfile: %w", err)
		}

		return stagingDir, filepath.ToSlash(input.relDockerfile), cleanup, nil
	}

	const stagedDockerfile = ".arcane.external.Dockerfile"
	stagedDockerfilePath := filepath.Join(stagingDir, stagedDockerfile)
	dockerfileInfo, err := os.Stat(input.fullDockerfilePath)
	if err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("failed to stat Dockerfile: %w", err)
	}

	err = copyFileWithMode(input.fullDockerfilePath, stagedDockerfilePath, dockerfileInfo.Mode())
	if err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("failed to stage Dockerfile: %w", err)
	}

	return stagingDir, stagedDockerfile, cleanup, nil
}

func (b *Service) performDockerBuild(
	ctx context.Context,
	dockerClient *dockerclient.Client,
	buildContext io.Reader,
	buildOpts dockerclient.ImageBuildOptions,
	progressWriter io.Writer,
) (retErr error) {
	buildSession, err := session.NewSession(ctx, "")
	if err != nil {
		return err
	}
	buildSession.Allow(authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: buildkitAuthConfigProvider(authprovider.LoadAuthConfig(config.LoadDefaultConfigFile(os.Stderr)), b.registryAuthProvider),
	}))
	// keep: buildOpts is a by-value copy; SessionID only known after session creation
	buildOpts.SessionID = buildSession.ID()

	sessionCtx, cancelSession := context.WithCancel(ctx)
	sessionErrCh := make(chan error, 1)
	go func() {
		sessionErrCh <- buildSession.Run(sessionCtx, dockerutils.SessionDialer(dockerClient))
	}()
	defer func() {
		cancelSession()
		retErr = errors.Join(retErr, buildSession.Close(), <-sessionErrCh)
	}()

	resp, err := dockerClient.ImageBuild(ctx, buildContext, buildOpts)
	if err != nil {
		return err
	}

	return errors.Join(
		renderDockerBuildStream(ctx, resp.Body, progressWriter),
		resp.Body.Close(),
	)
}

func (b *Service) pushDockerImages(
	ctx context.Context,
	dockerClient *dockerclient.Client,
	tags []string,
	progressWriter io.Writer,
) error {
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			continue
		}
		pushResp, pushErr := dockerClient.ImagePush(ctx, tag, dockerclient.ImagePushOptions{
			RegistryAuth: func() string {
				if b.registryAuthProvider == nil {
					return ""
				}
				authHeader, authErr := registryAuthHeaderForImage(ctx, b.registryAuthProvider, tag)
				if authErr != nil {
					_, _ = fmt.Fprintln(progressWriter, "registry auth unavailable for "+tag)
					return ""
				}
				return authHeader
			}(),
		})
		if pushErr != nil {
			return pushErr
		}
		if err := dockerutils.RenderJSONMessageStream(pushResp, progressWriter); err != nil {
			_ = pushResp.Close()
			return err
		}
		_ = pushResp.Close()
	}
	return nil
}

func buildDockerImageOptions(
	req types.BuildRequest,
	input dockerBuildInput,
	dockerfileForBuild string,
	authConfigs map[string]dockerregistry.AuthConfig,
) (dockerclient.ImageBuildOptions, error) {
	platformSpecs, parseErr := func() ([]ocispec.Platform, error) {
		if input.platform == "" {
			return nil, nil
		}
		platform, err := platforms.Parse(input.platform)
		if err != nil {
			return nil, err
		}
		return []ocispec.Platform{platform}, nil
	}()
	if parseErr != nil {
		return dockerclient.ImageBuildOptions{}, parseErr
	}

	return dockerclient.ImageBuildOptions{
		Version:     dockerbuild.BuilderBuildKit,
		Tags:        req.Tags,
		Dockerfile:  dockerfileForBuild,
		Remove:      true,
		ForceRemove: true,
		Target:      strings.TrimSpace(req.Target),
		BuildArgs:   input.buildArgs,
		Labels:      input.labels,
		CacheFrom:   input.cacheFrom,
		NoCache:     input.noCache,
		PullParent:  input.pullParent,
		NetworkMode: input.networkMode,
		Isolation:   dockercontainer.Isolation(input.isolation),
		ShmSize:     input.shmSize,
		Ulimits:     input.ulimits,
		ExtraHosts:  input.extraHosts,
		AuthConfigs: kit.Ternary(len(authConfigs) == 0, nil, authConfigs),
		Platforms:   platformSpecs,
	}, nil
}

func (b *Service) buildWithDocker(ctx context.Context, req types.BuildRequest, progressWriter io.Writer) (*types.BuildResult, error) {
	if b.dockerClientProvider == nil {
		return nil, &types.BuildDockerServiceUnavailableError{}
	}

	dockerClient, err := b.dockerClientProvider.GetClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	input, _, err := prepareDockerBuildInput(req)
	if err != nil {
		return nil, err
	}

	buildContextDir, dockerfileForBuild, cleanupBuildContext, err := prepareBuildContext(input.buildFilesystemInput)
	if err != nil {
		return nil, err
	}
	defer cleanupBuildContext()

	excludes, err := readDockerignore(buildContextDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}
	buildContext, err := archive.TarWithOptions(buildContextDir, &archive.TarOptions{ExcludePatterns: excludes})
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}
	defer func() { _ = buildContext.Close() }()

	buildOpts, buildOptsErr := buildDockerImageOptions(req, input, dockerfileForBuild, func() map[string]dockerregistry.AuthConfig {
		if b.registryAuthProvider == nil {
			return nil
		}
		loadedAuthConfigs, authErr := b.registryAuthProvider.GetAllRegistryAuthConfigs(ctx)
		if authErr != nil {
			_, _ = fmt.Fprintln(progressWriter, "registry auth unavailable for build")
			return nil
		}
		return loadedAuthConfigs
	}())
	if buildOptsErr != nil {
		return nil, buildOptsErr
	}

	err = b.performDockerBuild(ctx, dockerClient, buildContext, buildOpts, progressWriter)
	if err != nil {
		return nil, err
	}

	if req.Push {
		err = b.pushDockerImages(ctx, dockerClient, req.Tags, progressWriter)
		if err != nil {
			return nil, err
		}
	}

	return &types.BuildResult{
		Provider: "local",
		Tags:     req.Tags,
	}, nil
}

func readDockerignore(contextDir string) ([]string, error) {
	file, err := os.Open(filepath.Join(contextDir, ".dockerignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read .dockerignore: %w", err)
	}
	defer func() { _ = file.Close() }()

	patterns, err := ignorefile.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read .dockerignore: %w", err)
	}

	return patterns, nil
}

func dockerfileExcludedByDockerignore(contextDir, dockerfilePath string) (bool, error) {
	patterns, err := readDockerignore(contextDir)
	if err != nil {
		return false, err
	}
	if len(patterns) == 0 {
		return false, nil
	}

	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return false, fmt.Errorf("invalid .dockerignore: %w", err)
	}

	relDockerfile := filepath.ToSlash(filepath.Clean(dockerfilePath))
	if relDockerfile == "." || relDockerfile == "" {
		return false, nil
	}

	excluded, err := matcher.MatchesOrParentMatches(relDockerfile)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate .dockerignore for Dockerfile: %w", err)
	}

	return excluded, nil
}
