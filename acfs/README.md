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

The root package also provides `Stat`, `ReadTo`, `WriteFrom`, `MkdirAll`, and
`RemoveAll`. Public filesystem and protocol DTOs live in `types`.

## CLI

GoReleaser publishes the static `acfs` Linux binary for every architecture
used by the Arcane tools image:

```text
acfs list|walk|stat|read|write|mkdir|remove|version
```

`list` and `stat` emit JSON, `walk` emits NDJSON, and `read` emits an `ARCW`
protocol-v1 header followed by raw file content. Diagnostics are written only
to stderr.

## Development

```sh
just format
just vet
just test
just lint
just snapshot
just release         # increment the latest patch version
just release 1.2.3   # release an explicit version
```

`release` creates and pushes the version tag; the release workflow runs
GoReleaser and publishes the Linux binaries and checksum manifest.

## License

ACFS is released under the BSD 3-Clause License.
