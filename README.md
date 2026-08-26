<div align="center">

# kit

Shared Go packages and nested modules for Arcane.

<a href="https://pkg.go.dev/go.getarcane.app/kit"><img src="https://pkg.go.dev/badge/go.getarcane.app/kit.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/kit/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

The root module `go.getarcane.app/kit` holds small, focused leaf packages that
any Arcane Go module can depend on, such as
`go.getarcane.app/kit/pkg/utils/filesystem`. Larger projects live alongside it
as independently versioned nested modules — [`acfs`](acfs/README.md) is
`go.getarcane.app/acfs`, released with `acfs/vX.Y.Z` tags.

Nested modules may import kit, never the other way around, and shared code is
only added to the root when it has a concrete contract that more than one
module needs.

## Development

The committed `go.work` ties the modules together, so nested modules always
build against the checked-out kit packages. Because `./...` does not cross
module boundaries, repository-wide tasks run per module through the root
Justfile:

```sh
just format
just lint
just vet
just test
just test-race
```

Module-specific tasks such as ACFS benchmarks (`just benchmark-acfs`) and
GoReleaser (`just release-check`, `just snapshot`) live there as well.
Releases are cut per module with `just release acfs` or `just release kit`,
which derive the version bump from conventional commits via git-cliff, update
the module changelog, and push the `acfs/vX.Y.Z` or `vX.Y.Z` tag.

## License

kit is released under the BSD 3-Clause License.
