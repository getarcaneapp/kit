<div align="center">

# Arcane Updater

Portable Docker auto-update orchestration for Go applications.

<a href="https://github.com/getarcaneapp/updater/actions/workflows/ci.yml"><img src="https://github.com/getarcaneapp/updater/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<a href="https://pkg.go.dev/go.getarcane.app/updater"><img src="https://pkg.go.dev/badge/go.getarcane.app/updater.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/updater/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

Arcane Updater is the standalone Go module behind Arcane's Docker auto-updater. It pulls newer images, recreates the containers running them, and restarts whatever depended on those containers — with Compose awareness, registry digest checks, and a path for containers that must update themselves.

It works out of the box against the local Docker environment. Persistence, notifications, and self-upgrade behavior stay adapter-driven, so a host application plugs in only the parts it owns.

> [!IMPORTANT]
> This module is in early development. The API is not stable and may change without notice.

## Install

```sh
go get go.getarcane.app/updater@latest
```

## Quick start

Everything runs through a `Service`. The zero `Config` is valid:

```go
import "go.getarcane.app/updater"

service, err := updater.New(updater.Config{})
if err != nil {
	return err
}
defer service.Close()

result, err := service.UpdateContainer(ctx, containerID, updater.Options{})
```

To work through recorded pending updates instead of one named container:

```go
store := updater.NewMemoryPendingStore(updater.ImageUpdateRecord{
	ID:         "sha256:old-image-id",
	Repository: "nginx",
	Tag:        "1.27",
	HasUpdate:  true,
	UpdateType: updater.UpdateTypeDigest,
})

service, err := updater.New(updater.Config{PendingStore: store})
if err != nil {
	return err
}
defer service.Close()

result, err := service.ApplyPending(ctx, updater.Options{})
```

`ApplyPending` applies each pending update and then restarts the containers still running the images it replaced. `Options{DryRun: true}` reports what would change without touching anything.

## Configuring

Every `Config` field is a seam. Leave one nil and the updater supplies a Docker-backed default — or, for the purely optional ones, skips that behavior entirely. Override only what you own:

```go
service, err := updater.New(updater.Config{
	// Replace a default.
	DockerClientProvider: updater.NewDockerClientProvider(client.WithHost("tcp://docker-proxy:2375")),
	PendingStore:         myStore,

	// Add optional behavior.
	Notifier:      myNotifier,   // told about each successful update
	EventRecorder: myEvents,     // receives lifecycle events
	SelfUpdater:   myUpgrader,   // handles the container we run in

	// Restrict which containers are in scope.
	LabelPolicy: updater.LabelPolicy{
		IsUpdateDisabledFunc: func(l map[string]string) bool { return l["updates"] == "off" },
	},

	OperationTimeout: 30 * time.Second,
})
```

`LabelPolicy` merges per field: overriding `IsUpdateDisabledFunc` above keeps the default behavior for self-update, agent, server, swarm, and stop-signal detection. See `DefaultLabelPolicy`.

`Service.Close` shuts down the Docker client `New` created. If you supplied your own `DockerClientProvider`, its lifecycle stays yours and `Close` is a no-op.

## How it works

1. Resolve a pullable image reference for each in-scope container, skipping image IDs and digest-pinned references — re-pulling those can never yield a newer image.
2. Skip containers the `LabelPolicy` disables, containers the host excludes via `SettingsProvider`, and Swarm tasks.
3. Select a newer eligible version tag, or compare the configured tag's digest. `Options{Force: true}` bypasses unchanged-image checks while preserving the selection policy.
4. Pull the target image.
5. Recreate the container — through the `ProjectUpdater` for Compose services (grouped by project, then verified to no longer run the old image), or by a direct stop/create/start with rollback for standalone containers.
6. Restart containers that depend on what just changed, in dependency order.
7. Hand self-update targets to the `SelfUpdater`, last, since updating them may stop the calling process.

Scheduling, durable persistence, and user-facing notifications are not the module's job; supply adapters for those.

## Package layout

| Package | Contents |
| --- | --- |
| `updater` | The `Service`, `Config`, the port interfaces, and the result types. Everything a host application normally needs. |
| `updater/refs` | Image reference normalization and pullability checks. |
| `updater/digest` | OCI digest parsing and canonicalization. |
| `updater/labels` | The Arcane container labels and the predicates that read them. |
| `updater/registry` | Registry HTTP digest and pull-rate-limit lookups. |

Implementation details live under `internal/` and are not part of the public API.

## Migrating from v0.6.x

v0.7.0 collapses `api` and `types` into the module root, so one import replaces two.

| Before | After |
| --- | --- |
| `api.NewService(cfg)` | `updater.New(cfg)`, which now returns `(*Service, error)` |
| `api.NewDefaultService()` | `updater.New(updater.Config{})` |
| `api.Service`, `api.Config`, the port interfaces | same names in `updater` |
| `types.Options`, `types.Result`, `types.Status`, … | same names in `updater` |
| `api.ResolvePullableImageRef` | `refs.PullableImageRef`, without the second return |
| `api.UpdateStandaloneContainer` | unexported; use `UpdateContainer` |
| `pkg/digest.RemoteResolver` (`GetImageDigest`) | `updater.RegistryDigestResolver` (`ImageDigest`) |
| `pkg/labels.DefaultLabelPolicy` | `updater.DefaultLabelPolicy` |
| `pkg/labels.GetStopSignal` | `labels.StopSignal` |
| `pkg/registry.NewRegistryHTTPClient` | `registry.NewHTTPClient` |
| `pkg/{refs,digest,labels,registry}` | `{refs,digest,labels,registry}` — drop the `pkg/` |
| `pkg/{match,deps,utils}`, digest `Checker`/`RefIDCache` | moved to `internal/`; no longer public |
| `pkg/logs` | removed |
| `types.HistoryRecord`, `types.ContextHook` | removed; nothing consumed them |

Reshaped data types:

- `Result.StartTime`/`EndTime` are now `time.Time`; the pre-formatted `Duration` string is now the `Duration()` method. `ActivityID` is gone.
- `ResourceResult.OldImages`/`NewImages` maps are now the `OldImage`/`NewImage` strings they always held.
- `Options.Type` and `Options.ResourceIDs` are gone; nothing read them.
- `ResourceType`, `ResourceStatus`, and `UpdateType` are now named string types. The constant *values* are unchanged, so persisted data stays valid.

`New` also fixes a trap: `LabelPolicy` now merges per field, so overriding one func no longer silently drops the defaults for the others.

## Development

```sh
just format all
just test
just lint
```

## Tag-based updates

The default `auto` strategy discovers newer tags for stable, complete semantic
versions such as `3.1.2` and `v3.1.2`. Moving tags (`latest`), partial versions
(`3` or `3.1`), and ambiguous prerelease or variant tags keep using digest checks.
Set `strategy: digest` to keep a specific version tag and follow only its digest.

Add a constraint or pattern to control selection for a container or Compose service:

```yaml
services:
  app:
    image: example/app:3.1.2-alpine
    labels:
      com.getarcaneapp.arcane.updater.strategy: tag
      com.getarcaneapp.arcane.updater.constraint: '3.x'
      com.getarcaneapp.arcane.updater.tag-pattern: '(?P<version>\d+\.\d+\.\d+)-alpine'
```

An omitted strategy is equivalent to `strategy: auto`. A constraint or pattern
with `auto` requests tag selection; invalid rules or an incompatible current tag
return an error. Explicit `strategy: tag` always requires a valid tag policy.
Without an explicit constraint, tag updates stay within the current major, or the current
minor for `0.x`. An explicit constraint replaces that range. Version comparison
uses complete semantic versions with an optional `v` prefix. Prereleases require
an explicit constraint that admits them, such as `>=3.1.2-0 <4.0.0`.

Patterns match the entire tag. A named `version` capture extracts the semantic
version from a variant tag; otherwise the entire matched tag must be a semantic
version. Use a capture for tags such as `3.1.2-alpine` to keep the selected variant.
The updater preserves the exact registry tag when pulling. Equivalent candidate
versions use the lexically smallest tag, and never replace the current version
with an equal or older one. Invalid current versions or rules return an error.
If no newer tag qualifies, the current tag is still checked for digest changes.
Image IDs and digest-pinned references are ineligible, including with `Force`.

### Checking and recording candidates

`CheckContainerUpdate` returns a candidate without pulling, recreating, or writing
pending records. `CheckImageUpdate` accepts a `types.CheckRequest` for callers that
already have an image reference and policy. Supplying `CurrentDigest` avoids a
local Docker lookup for digest checks. Errors from tag listing are returned;
partial listings are never treated as an up-to-date result.

```go
check, err := service.CheckContainerUpdate(ctx, containerID)
if err != nil {
    return err
}
if check.UpdateAvailable {
    current, err := refs.NormalizeReference(check.CurrentRef)
    if err != nil {
        return err
    }
    target, err := refs.NormalizeReference(check.TargetRef)
    if err != nil {
        return err
    }
    record := updater.ImageUpdateRecord{
        ContainerID: check.ContainerID,
        Repository: current.RegistryHost + "/" + current.Repository,
        Tag: current.Tag,
        HasUpdate: true,
        UpdateType: updater.UpdateType(check.UpdateType),
        CurrentVersion: check.CurrentVersion,
        LatestVersion: &target.Tag,
    }
    // Persist record in your application's PendingStore, or pass it to
    // updater.NewMemoryPendingStore(record) when constructing a service.
    _ = record
}
```

Import `go.getarcane.app/updater/refs` for reference parsing and
`go.getarcane.app/updater/types` for check requests and policies. A host can
replace `Config.RegistryTagLister` to supply registry credentials or its own
registry client. The default uses Docker configuration credentials and handles
registry pagination with a 30-second total lookup deadline.

`ImageUpdateRecord.ContainerID` scopes a candidate to one container. `ID` retains
its existing image/store meaning. Durable stores must include `ContainerID` in
record identity so two containers sharing an image can follow different rules.
The built-in memory store already does this. Check results do not contain the
image ID; a host that keys records by image ID should supply it separately.

`ApplyPending` rechecks current references, eligibility, and constraints before
pulling. It pulls each distinct target once and retains records after failure or
stale configuration. Pending runs only update running containers; use
`UpdateContainer` for an explicit update of a stopped container. Legacy unscoped
tag records match the configured old reference, never every container sharing
its image ID. Conflicting pending targets for one container or Compose service
fail before pulls. Digest-only pending runs retain their existing behavior.

`UpdateContainer` uses the same tag selection. `DryRun` resolves and reports the
candidate without pulling or changing resources. `Force` can recreate an
unchanged image but cannot bypass eligibility or version rules. A tag change
still updates the configured reference when both tags share the same image ID.

### Compose host adapters

For Compose tag changes, implement `types.ProjectImageUpdater` on the configured
`ProjectUpdater`:

```go
UpdateServiceImages(ctx context.Context, projectID string,
    changes map[string]types.ServiceImageChange) error
```

Each service change contains `ExpectedRef` and `TargetRef`. The adapter must check
that the persisted service image still matches `ExpectedRef`, persist `TargetRef`,
and recreate the service. Return success only after persistence and recreation
succeed. The module groups changes by project and verifies every running service
container against the target reference and pulled image ID. An adapter error
retains the pending records even if some services changed.

The built-in CLI adapter supports digest updates only. Compose tag changes fail
before pulling unless a host adapter implements this interface, and never fall
back to standalone recreation. Self-update targets continue receiving the chosen
reference through `SelfUpdater`.

## License

Arcane Updater is released under the BSD 3-Clause License.
