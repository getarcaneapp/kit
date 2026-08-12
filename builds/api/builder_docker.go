package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/buildkit/session"
	"github.com/moby/go-archive"
	dockerbuild "github.com/moby/moby/api/types/build"
	dockercontainer "github.com/moby/moby/api/types/container"
	dockerregistry "github.com/moby/moby/api/types/registry"
	dockerclient "github.com/moby/moby/client"
	"github.com/moby/patternmatcher"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	dockerutils "go.getarcane.app/builds/pkg/utils/docker"
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

func parseUlimitsInternal(values map[string]string) []*dockercontainer.Ulimit {
	if len(values) == 0 {
		return nil
	}

	out := make([]*dockercontainer.Ulimit, 0, len(values))
	for name, raw := range values {
		name = strings.TrimSpace(name)
		raw = strings.TrimSpace(raw)
		if name == "" || raw == "" {
			continue
		}

		parts := strings.Split(raw, ":")
		if len(parts) == 1 {
			single, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
			if err != nil {
				continue
			}
			out = append(out, &dockercontainer.Ulimit{Name: name, Soft: single, Hard: single})
			continue
		}

		soft, softErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		hard, hardErr := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if softErr != nil || hardErr != nil {
			continue
		}

		out = append(out, &dockercontainer.Ulimit{Name: name, Soft: soft, Hard: hard})
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func prepareBuildFilesystemInputInternal(req types.BuildRequest) (buildFilesystemInput, error) {
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

	dockerfilePath := strings.TrimSpace(req.Dockerfile)
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}

	fullDockerfilePath := dockerfilePath
	if !filepath.IsAbs(dockerfilePath) {
		fullDockerfilePath = filepath.Join(contextDir, dockerfilePath)
	}

	relDockerfile, relErr := filepath.Rel(contextDir, fullDockerfilePath)
	dockerfileOutsideCtx := relErr != nil || strings.HasPrefix(relDockerfile, "..")
	if !dockerfileOutsideCtx {
		excluded, err := dockerfileExcludedByDockerignoreInternal(contextDir, relDockerfile)
		if err != nil {
			return buildFilesystemInput{}, err
		}
		dockerfileOutsideCtx = excluded
	}
	if dockerfileOutsideCtx {
		relDockerfile = filepath.Base(fullDockerfilePath)
	} else {
		relDockerfile = filepath.ToSlash(relDockerfile)
	}

	return buildFilesystemInput{
		contextDir:           contextDir,
		fullDockerfilePath:   fullDockerfilePath,
		relDockerfile:        relDockerfile,
		dockerfileOutsideCtx: dockerfileOutsideCtx,
	}, nil
}

func prepareDockerBuildInputInternal(req types.BuildRequest) (dockerBuildInput, bool, error) {
	fsInput, err := prepareBuildFilesystemInputInternal(req)
	if err != nil {
		return dockerBuildInput{}, false, err
	}

	if len(req.Platforms) > 1 {
		return dockerBuildInput{}, true, &types.DockerBuildMultiPlatformUnsupportedError{}
	}

	platform := ""
	if len(req.Platforms) == 1 {
		platform = strings.TrimSpace(req.Platforms[0])
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
	if len(labels) == 0 {
		labels = nil
	}

	cacheFrom := make([]string, 0, len(req.CacheFrom))
	for _, source := range req.CacheFrom {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		cacheFrom = append(cacheFrom, source)
	}
	if len(cacheFrom) == 0 {
		cacheFrom = nil
	}

	extraHosts := make([]string, 0, len(req.ExtraHosts))
	for _, host := range req.ExtraHosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		extraHosts = append(extraHosts, host)
	}
	if len(extraHosts) == 0 {
		extraHosts = nil
	}

	return dockerBuildInput{
		buildFilesystemInput: fsInput,
		platform:             platform,
		buildArgs:            buildArgs,
		labels:               labels,
		cacheFrom:            cacheFrom,
		noCache:              req.NoCache,
		pullParent:           req.Pull,
		networkMode:          strings.TrimSpace(req.Network),
		isolation:            strings.TrimSpace(req.Isolation),
		shmSize:              req.ShmSize,
		ulimits:              parseUlimitsInternal(req.Ulimits),
		extraHosts:           extraHosts,
	}, false, nil
}

func createBuildContextInternal(contextDir string) (io.ReadCloser, error) {
	excludes, err := readDockerignoreInternal(contextDir)
	if err != nil {
		return nil, err
	}
	return archive.TarWithOptions(contextDir, &archive.TarOptions{ExcludePatterns: excludes})
}

func copyFileWithModeInternal(src, dst string, mode os.FileMode) (copyErr error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err := out.Close(); copyErr == nil && err != nil {
			copyErr = err
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return nil
}

func copyContextTreeInternal(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		targetPath := filepath.Join(dstRoot, relPath)
		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, targetPath) //nolint:gosec // build context staging intentionally preserves symlinks from the user-selected source tree.
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		return copyFileWithModeInternal(path, targetPath, info.Mode())
	})
}

func prepareBuildContextInternal(input buildFilesystemInput) (string, string, func(), error) {
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

	if err := copyContextTreeInternal(input.contextDir, stagingDir); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("failed to stage build context: %w", err)
	}

	if input.dockerfileInline != "" {
		stagedDockerfilePath := filepath.Join(stagingDir, filepath.FromSlash(input.relDockerfile))
		if err := os.MkdirAll(filepath.Dir(stagedDockerfilePath), 0o755); err != nil {
			cleanup()
			return "", "", nil, fmt.Errorf("failed to create inline Dockerfile path: %w", err)
		}
		if err := os.WriteFile(stagedDockerfilePath, []byte(input.dockerfileInline), 0o600); err != nil {
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

	if err := copyFileWithModeInternal(input.fullDockerfilePath, stagedDockerfilePath, dockerfileInfo.Mode()); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("failed to stage Dockerfile: %w", err)
	}

	return stagingDir, stagedDockerfile, cleanup, nil
}

func prepareDockerBuildContextInternal(input dockerBuildInput) (string, string, func(), error) {
	return prepareBuildContextInternal(input.buildFilesystemInput)
}

func (b *Service) performDockerBuildInternal(
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
	buildSession.Allow(b.newBuildkitAuthProviderInternal())
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
		renderDockerBuildStreamInternal(ctx, resp.Body, progressWriter),
		resp.Body.Close(),
	)
}

func (b *Service) pushDockerImagesInternal(
	ctx context.Context,
	dockerClient *dockerclient.Client,
	tags []string,
	progressWriter io.Writer,
) error {
	if progressWriter == nil {
		progressWriter = io.Discard
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			continue
		}
		pushOptions := dockerclient.ImagePushOptions{}
		if b.registryAuthProvider != nil {
			authHeader, authErr := registryAuthHeaderForImageInternal(ctx, b.registryAuthProvider, tag)
			if authErr != nil {
				_, _ = fmt.Fprintln(progressWriter, "registry auth unavailable for "+tag)
			} else if authHeader != "" {
				pushOptions.RegistryAuth = authHeader
			}
		}
		pushResp, pushErr := dockerClient.ImagePush(ctx, tag, pushOptions)
		if pushErr != nil {
			return pushErr
		}
		if pushResp != nil {
			if err := dockerutils.RenderJSONMessageStream(pushResp, progressWriter); err != nil {
				_ = pushResp.Close()
				return err
			}
			_ = pushResp.Close()
		}
	}
	return nil
}

func parseOCIPlatformInternal(value string) (ocispec.Platform, error) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) < 2 {
		return ocispec.Platform{}, fmt.Errorf("invalid platform: %q", value)
	}

	platform := ocispec.Platform{
		OS:           strings.TrimSpace(parts[0]),
		Architecture: strings.TrimSpace(parts[1]),
	}
	if len(parts) >= 3 {
		platform.Variant = strings.TrimSpace(parts[2])
	}

	if platform.OS == "" || platform.Architecture == "" {
		return ocispec.Platform{}, fmt.Errorf("invalid platform: %q", value)
	}

	return platform, nil
}

func buildDockerImageOptionsInternal(
	req types.BuildRequest,
	input dockerBuildInput,
	dockerfileForBuild string,
	authConfigs map[string]dockerregistry.AuthConfig,
) (dockerclient.ImageBuildOptions, error) {
	buildOpts := dockerclient.ImageBuildOptions{
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
		AuthConfigs: authConfigs,
	}
	if len(buildOpts.AuthConfigs) == 0 {
		buildOpts.AuthConfigs = nil
	}

	if input.platform != "" {
		platform, parseErr := parseOCIPlatformInternal(input.platform)
		if parseErr != nil {
			return dockerclient.ImageBuildOptions{}, parseErr
		}
		buildOpts.Platforms = []ocispec.Platform{platform}
	}

	return buildOpts, nil
}

func (b *Service) buildWithDockerInternal(ctx context.Context, req types.BuildRequest, progressWriter io.Writer) (*types.BuildResult, error) {
	if b.dockerClientProvider == nil {
		return nil, &types.BuildDockerServiceUnavailableError{}
	}

	dockerClient, err := b.dockerClientProvider.GetClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	input, _, err := prepareDockerBuildInputInternal(req)
	if err != nil {
		return nil, err
	}

	buildContextDir, dockerfileForBuild, cleanupBuildContext, err := prepareDockerBuildContextInternal(input)
	if err != nil {
		return nil, err
	}
	defer cleanupBuildContext()

	buildContext, err := createBuildContextInternal(buildContextDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create build context: %w", err)
	}
	defer buildContext.Close()

	var authConfigs map[string]dockerregistry.AuthConfig
	if b.registryAuthProvider != nil {
		loadedAuthConfigs, authErr := b.registryAuthProvider.GetAllRegistryAuthConfigs(ctx)
		if authErr != nil {
			if progressWriter != nil {
				_, _ = fmt.Fprintln(progressWriter, "registry auth unavailable for build")
			}
		} else {
			authConfigs = loadedAuthConfigs
		}
	}

	buildOpts, buildOptsErr := buildDockerImageOptionsInternal(req, input, dockerfileForBuild, authConfigs)
	if buildOptsErr != nil {
		return nil, buildOptsErr
	}

	if err := b.performDockerBuildInternal(ctx, dockerClient, buildContext, buildOpts, progressWriter); err != nil {
		return nil, err
	}

	if req.Push {
		if err := b.pushDockerImagesInternal(ctx, dockerClient, req.Tags, progressWriter); err != nil {
			return nil, err
		}
	}

	return &types.BuildResult{
		Provider: "local",
		Tags:     req.Tags,
	}, nil
}

func readDockerignoreInternal(contextDir string) ([]string, error) {
	ignorePath := filepath.Join(contextDir, ".dockerignore")
	file, err := os.Open(ignorePath)
	if err != nil {
		return nil, nil
	}
	defer file.Close()

	patterns := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read .dockerignore: %w", err)
	}

	return patterns, nil
}

func dockerfileExcludedByDockerignoreInternal(contextDir, dockerfilePath string) (bool, error) {
	patterns, err := readDockerignoreInternal(contextDir)
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
