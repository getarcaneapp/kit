package api

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	depotbuild "github.com/depot/depot-go/build"
	depotmachine "github.com/depot/depot-go/machine"
	cliv1 "github.com/depot/depot-go/proto/depot/cli/v1"
	"github.com/moby/buildkit/client"
	"go.getarcane.app/builds/types"
)

type buildSession struct {
	Client *client.Client
	Close  func(buildErr error) error
}

type buildProvider interface {
	Name() string
	NewSession(ctx context.Context, req types.BuildRequest) (*buildSession, error)
}

type depotBuildKitProvider struct {
	settings types.SettingsProvider
}

func newDepotBuildKitProviderInternal(settings types.SettingsProvider) *depotBuildKitProvider {
	return &depotBuildKitProvider{settings: settings}
}

func (p *depotBuildKitProvider) Name() string {
	return "depot"
}

func (p *depotBuildKitProvider) NewSession(ctx context.Context, req types.BuildRequest) (*buildSession, error) {
	if p.settings == nil {
		return nil, &types.BuildSettingsProviderUnavailableError{}
	}

	settings := p.settings.BuildSettings()
	projectID := strings.TrimSpace(settings.DepotProjectId)
	token := strings.TrimSpace(settings.DepotToken)
	if projectID == "" || token == "" {
		return nil, &types.DepotProjectCredentialsRequiredError{}
	}

	buildReq := &cliv1.CreateBuildRequest{ProjectId: projectID}
	build, err := depotbuild.NewBuild(ctx, buildReq, token)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Depot build: %w", err)
	}

	arch := selectDepotArchInternal(req.Platforms)
	machine, err := depotmachine.Acquire(ctx, build.ID, build.Token, arch)
	if err != nil {
		build.Finish(err)
		return nil, fmt.Errorf("failed to acquire Depot BuildKit machine: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	bk, err := machine.Connect(connectCtx)
	if err != nil {
		_ = machine.Release()
		build.Finish(err)
		return nil, fmt.Errorf("failed to connect to Depot BuildKit: %w", err)
	}

	return &buildSession{
		Client: bk,
		Close: func(buildErr error) error {
			build.Finish(buildErr)
			releaseErr := machine.Release()
			closeErr := bk.Close()
			return errors.Join(releaseErr, closeErr)
		},
	}, nil
}

func selectDepotArchInternal(platforms []string) string {
	for _, platform := range platforms {
		p := strings.ToLower(strings.TrimSpace(platform))
		switch {
		case strings.Contains(p, "arm64"):
			return "arm64"
		case strings.Contains(p, "amd64"):
			return "amd64"
		}
	}

	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}
