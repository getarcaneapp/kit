<div align="center">

# ACFS

Root-confined filesystem operations for Arcane and other Go applications.

<a href="https://github.com/getarcaneapp/acfs/actions/workflows/ci.yml"><img src="https://github.com/getarcaneapp/acfs/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<a href="https://pkg.go.dev/go.getarcane.app/acfs"><img src="https://pkg.go.dev/badge/go.getarcane.app/acfs.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/acfs/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

ACFS provides efficient directory listing, recursive walking, metadata, and
streaming file operations without spawning a process for every entry. Logical
paths are confined with `os.Root`, and directory traversal never follows
symlinks.

> [!IMPORTANT]
> This module is in early development. The API is not stable and may change
> before v1.0.0.

## Getting started

```sh
go get go.getarcane.app/acfs@latest
```

```go
entries, err := acfs.List(ctx, "/var/lib/docker/volumes/example/_data", "/")
if err != nil {
	return err
}

err = acfs.Walk(ctx, root, "/logs", func(entry types.Entry) error {
	fmt.Println(entry.Path, entry.Mode, entry.Size)
	return nil
})
```

The root package also provides `WalkBounded`, `Stat`, `ReadTo`, `WriteFrom`,
`MkdirAll`, `RemoveAll`, and ordered batch `Apply`. Public filesystem and
protocol DTOs live in `types`.

## CLI

GoReleaser publishes the static `acfs` Linux binary for every architecture
used by the Arcane tools image:

```text
acfs list|walk|stat|read|write|mkdir|remove|apply|version
```

`list`, `stat`, and `apply` emit JSON. `walk` emits NDJSON with a mandatory end
record and supports depth and entry limits. `read` emits an `ARCW` protocol-v2
header followed by raw file content. Failures are emitted as structured JSON
only on stderr.

## Development

```sh
just format
just test
just lint
```

`release` creates and pushes the version tag; the release workflow runs
GoReleaser and publishes the Linux binaries and checksum manifest.

## License

ACFS is released under the BSD 3-Clause License.
