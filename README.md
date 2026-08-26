# kit

Shared Go packages for Arcane, plus independently versioned nested modules.

## Layout

This repository hosts two Go modules:

| Path | Module | Purpose |
| --- | --- | --- |
| `.` | `go.getarcane.app/kit` | Shared leaf packages usable by any Arcane Go module. |
| `acfs` | `go.getarcane.app/acfs` | Arcane Container File System library and CLI (see [acfs/README.md](acfs/README.md)). |

## Rules

- **Dependency direction.** Nested modules may depend on `go.getarcane.app/kit`. The root
  module must never import a nested module.
- **Shared packages are leaf packages.** Code moves into the root module only when it has a
  concrete, module-independent contract (for example
  `go.getarcane.app/kit/pkg/utils/filesystem`). No catch-all `utils` package, no speculative
  helpers.
- **Modules stay independently valid.** Each module builds, tests, and tags on its own.
  Nested modules pin published `kit` versions in their `go.mod`; filesystem `replace`
  directives are never committed.

## Development

`go ./...` commands do not cross nested module boundaries, so repository-wide tasks run per
module through the root [Justfile](Justfile):

```
just format     # gofmt every module
just vet        # go vet every module
just lint       # custom golangci-lint over every module
just test       # go test every module
just test-race  # go test -race every module
```

The committed [go.work](go.work) makes nested modules build and test against the checked-out
root packages, both locally and in CI. To verify a module standalone, exactly as consumers
see it (requires the pinned `kit` version to be published), disable the workspace:

```
GOWORK=off go test ./...
```

Module-specific tasks (benchmarks, GoReleaser) live in each module's own Justfile, for
example [acfs/Justfile](acfs/Justfile).
