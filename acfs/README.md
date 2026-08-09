<div align="center">

# ACFS

Root-confined filesystem operations for Arcane, with the `acfs` streaming CLI.

<a href="https://github.com/getarcaneapp/acfs/actions/workflows/ci.yml"><img src="https://github.com/getarcaneapp/acfs/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<a href="https://pkg.go.dev/go.getarcane.app/acfs"><img src="https://pkg.go.dev/badge/go.getarcane.app/acfs.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/acfs/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

ACFS provides efficient directory enumeration and streaming file
operations without spawning a process for every entry. It accepts logical paths
rooted at `/`, confines every operation with `os.Root`, and deliberately avoids
following symlinks while listing or walking directory trees.

> [!IMPORTANT]
> This module is in early development. The API is not stable and may change
> before v1.0.0.

## Install

```sh
go get go.getarcane.app/acfs@latest
```

The standalone Linux binary is published as `acfs` by GoReleaser for
amd64, 386, arm/v7, arm64, ppc64le, and s390x.

## Library

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

The root package exposes:

- `List` for sorted direct-child metadata and `ListEach` for bounded-memory
  streaming of the same deterministic result.
- `Walk` for deterministic, sequential, non-symlink-following traversal.
- `Stat` for final-component `lstat` behavior.
- `OpenRead` and `ReadTo` for bounded streaming reads.
- `WriteFrom` for exact-size, temporary-sibling, atomic writes.
- `MkdirAll` and `RemoveAll` for root-confined mutation. `RemoveAll` rejects `/`.

Logical paths must start with `/`. Empty components, `.`, `..`, and NUL bytes
are rejected. Relative symlinks and absolute `/volume` symlinks can resolve
inside the root. Other absolute symlinks are external and cannot be followed.
Resolution stops after 40 links.

## CLI

```text
acfs list   --root <directory> --path <logical-path>
acfs walk   --root <directory> --path <logical-path>
acfs stat   --root <directory> --path <logical-path>
acfs read   --root <directory> --path <logical-path> [--limit <bytes>]
acfs write  --root <directory> --path <logical-path> --size <bytes> [--mode 0644]
acfs mkdir  --root <directory> --path <logical-path> [--mode 0755]
acfs remove --root <directory> --path <logical-path>
acfs version
```

`list` and `stat` write versioned JSON. `walk` writes one versioned NDJSON
record per entry. Diagnostics are written only to stderr.

`read` writes a 16-byte header followed by raw bytes:

| Offset | Size | Value |
| --- | ---: | --- |
| 0 | 4 | ASCII `ARCW` |
| 4 | 1 | Protocol version `1` |
| 5 | 3 | Reserved zero bytes |
| 8 | 8 | Unsigned big-endian payload length |

`write` consumes raw stdin and requires exactly the declared byte count. Short
or excess input leaves an existing destination unchanged.

## Development

```sh
just format
just vet
just test
just test-race
just lint
just release-check
just snapshot
```

`snapshot` creates local GoReleaser artifacts under `dist/`; releases and tags
remain maintainer-managed.

## License

ACFS is released under the BSD 3-Clause License.
