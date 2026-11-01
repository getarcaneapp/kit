<div align="center">

# kit

Shared Go packages and nested modules for Arcane.

<a href="https://pkg.go.dev/kit.getarcane.app"><img src="https://pkg.go.dev/badge/kit.getarcane.app.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/kit/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

> [!IMPORTANT]
> All modules are under development. The API may change at anytime before v1.0.0.

## Modules

| Directory        | Module                             |
| ---------------- | ---------------------------------- |
| `kit`            | `kit.getarcane.app`                |
| `acfs`           | `kit.getarcane.app/acfs`           |
| `builds`         | `kit.getarcane.app/builds`         |
| `docker`         | `kit.getarcane.app/docker`         |
| `docker/convert` | `kit.getarcane.app/docker/convert` |
| `sys/cgroup`     | `kit.getarcane.app/sys/cgroup`     |
| `tripwire`       | `kit.getarcane.app/tripwire`       |
| `updater`        | `kit.getarcane.app/updater`        |

Packages inside a module import by their path under it, e.g. `kit/sys/crypto` is
`kit.getarcane.app/sys/crypto` and `docker/streams/logs` is
`kit.getarcane.app/docker/streams/logs`.

Modules may import kit, never the other way around, and shared code is only
added to kit when it has a concrete contract that more than one module needs.

## License

kit is released under the BSD 3-Clause License.
