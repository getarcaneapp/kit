// Package types defines public build engine request, response, and event contracts.
package types

import (
	"context"
	"io"

	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
)

type (
	// BuildSettings configures build provider behavior.
	BuildSettings struct {
		DepotProjectID   string
		DepotToken       string
		BuildProvider    string
		BuildTimeoutSecs int
	}

	// BuildRequest contains options for building an image with BuildKit.
	BuildRequest struct {
		BuildArgs        map[string]string `json:"buildArgs,omitempty" doc:"Build arguments"`
		Labels           map[string]string `json:"labels,omitempty" doc:"Build labels"`
		Ulimits          map[string]string `json:"ulimits,omitempty" doc:"Build ulimits"`
		Network          string            `json:"network,omitempty" doc:"Build network mode"`
		Dockerfile       string            `json:"dockerfile,omitempty" doc:"Dockerfile path"`
		DockerfileInline string            `json:"dockerfileInline,omitempty" doc:"Inline Dockerfile content"`
		Provider         string            `json:"provider,omitempty" doc:"Build provider override"`
		Target           string            `json:"target,omitempty" doc:"Target stage"`
		ContextDir       string            `json:"contextDir" minLength:"1" doc:"Build context directory or Git URL"`
		Isolation        string            `json:"isolation,omitempty" doc:"Build isolation mode"`
		Entitlements     []string          `json:"entitlements,omitempty" doc:"Build entitlements"`
		CacheTo          []string          `json:"cacheTo,omitempty" doc:"Build cache targets"`
		CacheFrom        []string          `json:"cacheFrom,omitempty" doc:"Build cache sources"`
		ExtraHosts       []string          `json:"extraHosts,omitempty" doc:"Build extra host mappings"`
		Platforms        []string          `json:"platforms,omitempty" doc:"Target platforms"`
		Tags             []string          `json:"tags,omitempty" doc:"Image tags"`
		ShmSize          int64             `json:"shmSize,omitempty" doc:"Build shared memory size in bytes"`
		Pull             bool              `json:"pull,omitempty" doc:"Always pull referenced base images"`
		NoCache          bool              `json:"noCache,omitempty" doc:"Disable build cache"`
		Privileged       bool              `json:"privileged,omitempty" doc:"Enable privileged build"`
		Push             bool              `json:"push,omitempty" doc:"Push image"`
		Load             bool              `json:"load,omitempty" doc:"Load image into local Docker"`
	}

	// BuildResult provides basic build output metadata.
	BuildResult struct {
		Provider string   `json:"provider"`
		Digest   string   `json:"digest,omitempty"`
		Tags     []string `json:"tags,omitempty"`
	}

	// SettingsProvider provides build settings owned by the host application.
	SettingsProvider interface {
		BuildSettings() BuildSettings
	}

	// DockerClientProvider provides Docker clients.
	DockerClientProvider interface {
		GetClient(ctx context.Context) (*client.Client, error)
	}

	// RegistryAuthProvider provides registry auth configs for build and push operations.
	RegistryAuthProvider interface {
		GetAllRegistryAuthConfigs(ctx context.Context) (map[string]registry.AuthConfig, error)
	}

	// Builder builds container images.
	//
	// progressWriter receives the raw text the docker CLI would print for the
	// build (plain-mode BuildKit progress, jsonmessage-rendered push/load output).
	// Writes to it are serialized by the implementation, so any plain io.Writer
	// is safe to pass. Hosts that need structured framing wrap the writer
	// themselves.
	Builder interface {
		BuildImage(ctx context.Context, req BuildRequest, progressWriter io.Writer, serviceName string) (*BuildResult, error)
	}

	// LogCapture stores build output for history records.
	LogCapture interface {
		io.Writer
		String() string
		Truncated() bool
	}
)
