<div align="center">

# ACFS

Root-confined filesystem operations for Arcane and other Go applications.

<a href="https://github.com/getarcaneapp/acfs/actions/workflows/ci.yml"><img src="https://github.com/getarcaneapp/acfs/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<a href="https://pkg.go.dev/kit.getarcane.app/acfs"><img src="https://pkg.go.dev/badge/kit.getarcane.app/acfs.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/acfs/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

ACFS provides efficient directory listing, recursive walking, metadata, and
streaming file operations without spawning a process for every entry. Logical
paths are confined with `os.Root`, and directory traversal never follows
symlinks.

> [!IMPORTANT]
> This module is under development. The API is not stable and may change before v1.0.0.

## Getting started

```sh
go get kit.getarcane.app/acfs@latest
```

```go
root := acfs.Root{Path: "/var/lib/docker/volumes/example/_data"}
entries, err := root.List(ctx, "/")
if err != nil {
	return err
}

_, err = root.Walk(ctx, "/logs", types.WalkOptions{}, func(entry types.Entry) error {
	fmt.Println(entry.Path, entry.Mode, entry.Size)
	return nil
})
```

`Root` confines every operation to `Root.Path` using logical paths. `Host`
(`acfs.Host{}`) offers the same methods on absolute host paths without
confinement, and the `FS` interface covers what both share:

- Reading: `List`, `ListEach`, `Walk`, `Stat`, `Exists`, `OpenRead`,
  `OpenReadSeek`, `ReadFile`, `ReadTo`
- Writing: `Write`, `WriteFrom`, `WriteAt`, `OpenFile`, `CreateTemp`
- Directories and names: `Mkdir`, `MkdirAll`, `MkdirTemp`, `Rename`, `Remove`,
  `RemoveAll`
- Attributes and links: `Chmod`, `Chown`, `Lchown`, `Chtimes`, `Symlink`,
  `Readlink`

`Root` adds `LogicalPath`, `CopyTo`, `MirrorTo`, and ordered batch `Apply`;
`Host` adds `CopyDir` and `MirrorDir`. Public filesystem and protocol DTOs live
in `types`.

On a `Root`, links inside the root are handled per operation:

- Resolve links in the parent path: `Write`, `WriteFrom`, `WriteAt`, `Mkdir`,
  `MkdirAll`, `MkdirTemp`, `CreateTemp`, `Rename`, `Remove`, `RemoveAll`, and a
  read-only `OpenFile`.
- Refuse any link in the path with `ErrSymlink`: `Chmod`, `Chown`, `Lchown`,
  `Chtimes`, `Symlink`, a writing `OpenFile`, and `Apply`.
- A final link is refused by `Write` with `InPlace`, a writing `OpenFile`,
  `Chmod`, `Chown`, and `Chtimes`. `Lchown`, `Remove`, `RemoveAll`, and
  `Rename` act on the link itself.

`Symlink` only creates links that stay inside the root, stored as relative
targets. On Linux mutations act through a parent directory descriptor opened
without following symbolic links, so concurrent writers cannot redirect them.

### Threat model

- `Root`'s own operations never read, write, or follow a link outside the root.
- A process that can concurrently write inside the root can already create
  links and replace files there, so ACFS does not protect root contents from it.
- ACFS will not act as a confused deputy for such a process. An atomic write or
  `Apply` that finds its staged file or published name replaced returns
  `ErrReplaced`, not success. This narrows the race window but does not close
  it.
- A directory that ACFS has opened and another process then moves outside the
  root stays writable through the open descriptor until the operation finishes.

`OpenFile` takes typed options instead of `os.O_*` flags:

```go
lock, err := acfs.Host{}.OpenFile(ctx, "/run/arcane/lock", acfs.OpenOptions{
	Write: true, Create: true, Exclusive: true, Mode: 0o600,
})
```

Use typed options to choose how a byte-slice write replaces a file:

```go
err = root.Write(ctx, "/config.yaml", data, acfs.WriteOptions{Mode: 0o644})
err = root.Write(ctx, "/config.yaml", data, acfs.WriteOptions{Mode: 0o644, InPlace: true})
```

Writes are atomic by default. `InPlace` preserves existing file inodes for bind
mounts and refuses final symlinks; callers must handle partial writes on failure.

## CLI

GoReleaser publishes the static `acfs` Linux binary for every architecture
used by the Arcane tools image:

```text
acfs list|walk|stat|read|write|mkdir|remove|apply|version
```

`list`, `stat`, and `apply` emit JSON. `list --limit N` emits at most `N`
entries and adds `"truncated":true` when it left any out. `walk` emits NDJSON with a mandatory end
record and supports depth and entry limits. `read` emits an `ARCW` protocol-v2
header followed by raw file content. Failures are emitted as structured JSON
only on stderr.

## License

ACFS is released under the BSD 3-Clause License.
