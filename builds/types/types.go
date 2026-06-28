// Package types defines public build engine request, response, and event contracts.
package types

import (
	"context"
	"io"

	dockerregistry "github.com/moby/moby/api/types/registry"
	dockerclient "github.com/moby/moby/client"
)

// BuildSettings configures build provider behavior.
type BuildSettings struct {
	DepotProjectId   string
	DepotToken       string
	BuildProvider    string
	BuildTimeoutSecs int
}

// BuildRequest contains options for building an image with BuildKit.
type BuildRequest struct {
	ContextDir       string            `json:"contextDir" minLength:"1" doc:"Build context directory or Git URL"`
	Dockerfile       string            `json:"dockerfile,omitempty" doc:"Dockerfile path"`
	DockerfileInline string            `json:"dockerfileInline,omitempty" doc:"Inline Dockerfile content"`
	Tags             []string          `json:"tags,omitempty" doc:"Image tags"`
	Target           string            `json:"target,omitempty" doc:"Target stage"`
	BuildArgs        map[string]string `json:"buildArgs,omitempty" doc:"Build arguments"`
	Labels           map[string]string `json:"labels,omitempty" doc:"Build labels"`
	CacheFrom        []string          `json:"cacheFrom,omitempty" doc:"Build cache sources"`
	CacheTo          []string          `json:"cacheTo,omitempty" doc:"Build cache targets"`
	NoCache          bool              `json:"noCache,omitempty" doc:"Disable build cache"`
	Pull             bool              `json:"pull,omitempty" doc:"Always pull referenced base images"`
	Network          string            `json:"network,omitempty" doc:"Build network mode"`
	Isolation        string            `json:"isolation,omitempty" doc:"Build isolation mode"`
	ShmSize          int64             `json:"shmSize,omitempty" doc:"Build shared memory size in bytes"`
	Ulimits          map[string]string `json:"ulimits,omitempty" doc:"Build ulimits"`
	Entitlements     []string          `json:"entitlements,omitempty" doc:"Build entitlements"`
	Privileged       bool              `json:"privileged,omitempty" doc:"Enable privileged build"`
	ExtraHosts       []string          `json:"extraHosts,omitempty" doc:"Build extra host mappings"`
	Platforms        []string          `json:"platforms,omitempty" doc:"Target platforms"`
	Push             bool              `json:"push,omitempty" doc:"Push image"`
	Load             bool              `json:"load,omitempty" doc:"Load image into local Docker"`
	Provider         string            `json:"provider,omitempty" doc:"Build provider override"`
}

// BuildResult provides basic build output metadata.
type BuildResult struct {
	Provider string   `json:"provider"`
	Tags     []string `json:"tags,omitempty"`
	Digest   string   `json:"digest,omitempty"`
}

// ProgressDetail provides byte progress information for stream events.
type ProgressDetail struct {
	Current int64 `json:"current,omitempty"`
	Total   int64 `json:"total,omitempty"`
}

// ProgressEvent is the standardized NDJSON envelope for build streams.
type ProgressEvent struct {
	Type           string          `json:"type,omitempty"`
	Phase          string          `json:"phase,omitempty"`
	Service        string          `json:"service,omitempty"`
	Status         string          `json:"status,omitempty"`
	ID             string          `json:"id,omitempty"`
	ProgressDetail *ProgressDetail `json:"progressDetail,omitempty"`
	Error          string          `json:"error,omitempty"`
}

// SettingsProvider provides build settings owned by the host application.
type SettingsProvider interface {
	BuildSettings() BuildSettings
}

// DockerClientProvider provides Docker clients.
type DockerClientProvider interface {
	GetClient(ctx context.Context) (*dockerclient.Client, error)
}

// RegistryAuthProvider provides registry auth configs for build and push operations.
type RegistryAuthProvider interface {
	GetAllRegistryAuthConfigs(ctx context.Context) (map[string]dockerregistry.AuthConfig, error)
}

// Builder builds container images.
type Builder interface {
	BuildImage(ctx context.Context, req BuildRequest, progressWriter io.Writer, serviceName string) (*BuildResult, error)
}

// LogCapture stores build output for history records.
type LogCapture interface {
	io.Writer
	String() string
	Truncated() bool
}
