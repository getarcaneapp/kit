<div align="center">

# Arcane Builds

Portable Docker and BuildKit image build orchestration for Go applications.

<a href="https://pkg.go.dev/go.getarcane.app/builds"><img src="https://pkg.go.dev/badge/go.getarcane.app/builds.svg" alt="Go Reference"></a>
<a href="https://goreportcard.com/report/go.getarcane.app/builds"><img src="https://goreportcard.com/badge/go.getarcane.app/builds" alt="Go Report Card"></a>
<a href="https://github.com/getarcaneapp/builds/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

Arcane Builds is the standalone Go module behind Arcane's image build flow. It provides the build engine, Docker Engine build fallback, local Docker BuildKit integration, Depot BuildKit provider integration, registry auth wiring, build progress events, log capture, and reusable helper packages.

The module can be used directly in non-Arcane Go applications. Arcane-specific build history, activity logging, Git credential lookup, remote Git clone/probe/cleanup, and pagination remain host-application behavior. The build engine expects a local build context directory by the time `BuildImage` is called.

## How it works

Using Arcane Builds is a small image build flow:

1. Create an `api.Service` with `api.Config`.
2. Provide a `SettingsProvider`, a `DockerClientProvider`, and optionally a `RegistryAuthProvider`.
3. Call `BuildImage` with a `types.BuildRequest`, progress writer, and optional service name.
4. The service resolves the effective provider from the request override or host settings.
5. Local builds use Docker Engine build unless the Dockerfile requires BuildKit features.
6. BuildKit builds stream status as NDJSON `types.ProgressEvent` payloads and can load, push, or export image results depending on request options.
7. Registry auth configs are passed through to Docker build, BuildKit sessions, and image push operations.
8. Results are returned as `types.BuildResult`.

The build engine does not own durable persistence, Git repository credentials, remote Git checkout, user notifications, scheduling, or build history. Those can be added by the host application around the service.

## Getting started

```sh
go get go.getarcane.app/builds@latest
```

```go
type settingsProvider struct{}

func (settingsProvider) BuildSettings() types.BuildSettings {
	return types.BuildSettings{
		BuildProvider:    "local",
		BuildTimeoutSecs: 1800,
	}
}

svc := api.NewService(api.Config{
	SettingsProvider:     settingsProvider{},
	DockerClientProvider: dockerProvider,
	RegistryAuthProvider: registryProvider,
})

result, err := svc.BuildImage(ctx, types.BuildRequest{
	ContextDir: "/workspace/app",
	Dockerfile: "Dockerfile",
	Tags:       []string{"example/app:latest"},
	Load:       true,
}, progressWriter, "app")
```

For remote Git build contexts, parse the source with `pkg/utils/contextsource`, clone or prepare the repository in the host application, then pass the resolved local directory to `BuildImage`.

## Package layout

- `api`: public build service, adapter interfaces, provider selection, build execution, and log capture.
- `types`: stable public DTOs for build settings, requests, results, progress events, and build-specific errors.
- `pkg/utils/contextsource`: Git build context parsing, validation, repository URL normalization, and probe detection helpers.
- `pkg/utils/docker`: Docker BuildKit client options and Docker JSON message stream draining.

## Development

```sh
go test ./...
gofmt -w .
golangci-lint run ./...
```

## License

Arcane Builds is released under the BSD 3-Clause License.
