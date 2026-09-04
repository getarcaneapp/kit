<div align="center">

# kit

Shared Go packages and nested modules for Arcane.

<a href="https://pkg.go.dev/go.getarcane.app/kit"><img src="https://pkg.go.dev/badge/go.getarcane.app/kit.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/kit/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

> [!IMPORTANT]
> All modules are under development. The API is not stable and may change at anytime
> before v1.0.0.

Nested modules may import kit, never the other way around, and shared code is
only added to the root when it has a concrete contract that more than one
module needs.

## Releases

Release every module with a patch or minor bump from its own latest tag:

```sh
just release all --patch --test
just release all --patch
just release all --minor
```

`all` includes the root `kit` module and every nested module listed in the
Justfile. It releases them sequentially, asks for confirmation for each module,
and stops if a release fails or is canceled. Each release updates its changelog,
commits it, tags and pushes the release, and creates a GitHub draft release.
Commit your dependency updates before running a release so the tags include them.

Use `--test` to preview without writing files or creating releases. Release commands
require git-cliff and an authenticated GitHub CLI, including for previews.

You can also release a single module, such as `just release updater --patch`,
or set an exact version with `just release updater 0.8.1`. Without a bump flag,
the recipe chooses minor for `feat` commits or patch for `fix` commits. Use
`--patch` or `--minor` to release dependency updates even when there are no
`feat` or `fix` commits. Bump flags cannot be combined with each other or with
an explicit version, and `all` does not accept an explicit version.

## License

kit is released under the BSD 3-Clause License.
