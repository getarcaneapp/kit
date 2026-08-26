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

## License

kit is released under the BSD 3-Clause License.
