## docker/v0.3.2

### Bug fixes

* reject mount subpaths that escape the mount([6082ef2](https://github.com/getarcaneapp/kit/commit/6082ef2d72f05efab42c03453ffb13ccf156d3bc) by @kmendell)
* cap log line length in ReadLogLines([b666a39](https://github.com/getarcaneapp/kit/commit/b666a398c01eb6339c379751ae8cfa5d0ee6849d) by @kmendell)
* stream snapshot logs instead of buffering per stream([80d1109](https://github.com/getarcaneapp/kit/commit/80d11092f952fe42147cb7b3d445ca07e0ff1138) by @kmendell)
* require an io.ReadCloser in StreamMultiplexedLogs([9e0386d](https://github.com/getarcaneapp/kit/commit/9e0386d231cd4874f2872ba6480b1e85d06810d7) by @kmendell)
* discard exec output when no writer is set([3386f94](https://github.com/getarcaneapp/kit/commit/3386f943e3f1b27e4957754925e6231b387beddd) by @kmendell)
* keep bind propagation when mirroring mounts([7d1da76](https://github.com/getarcaneapp/kit/commit/7d1da76f6938afb269ac184e50bfed85ca61a5ea) by @kmendell)
* decode repaired inspect payloads with json v1 semantics([136026b](https://github.com/getarcaneapp/kit/commit/136026b200d07b719c43b207ef13233c27ca789c) by @kmendell)
* resolve zero stats sample time to now before storing([f0f9c86](https://github.com/getarcaneapp/kit/commit/f0f9c8658582623878e8b0f7aa61e31e406cdbbd) by @kmendell)
* encode invalid utf-8 instead of dropping json frames([aab6970](https://github.com/getarcaneapp/kit/commit/aab6970740755a593361de192d8fcf991bf73984) by @kmendell)
* wait for removed pollers to exit before reconciling([e80cced](https://github.com/getarcaneapp/kit/commit/e80cced494255d1438fd0a8625a2e763bddfa28a) by @kmendell)
* drop hub entries when their runner exits([b1fc5c4](https://github.com/getarcaneapp/kit/commit/b1fc5c423802ab8380cb248b98cd8f313b346d1f) by @kmendell)
* inline attrs of empty-key slog groups([0c65b2c](https://github.com/getarcaneapp/kit/commit/0c65b2c74116c4636a24054b3e8242bf959bb8fb) by @kmendell)
* bump deps([a7c1bfe](https://github.com/getarcaneapp/kit/commit/a7c1bfea542b3fb145814e95a2ec01de83791002) by @kmendell)

### Performance improvements

* skip attr map allocation for attr-less log records([7a41670](https://github.com/getarcaneapp/kit/commit/7a416700989f7c1399837211e015d566972c98f1) by @kmendell)

### Other

* drop ReadAllLogs and the unused Follow log option([8753fbf](https://github.com/getarcaneapp/kit/commit/8753fbf0cbcedb051628e4c8045db1edbbbcd301) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.3.1...docker/v0.3.2

## docker/v0.3.1

### Bug fixes

* bump golang deps([7efb0f2](https://github.com/getarcaneapp/kit/commit/7efb0f2fab12e5bcddc7b82435f0df3606700d3d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.3.0...docker/v0.3.1

## docker/v0.3.0

### New features

* add CopyToContainer and CopyFromContainer archive helpers([86294c5](https://github.com/getarcaneapp/kit/commit/86294c58b0e6bdb42c2fd90501bfa81d1da2f222) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.2.0...docker/v0.3.0

## docker/v0.2.0

### New features

* move to kit.getarcane.app/docker([76581a3](https://github.com/getarcaneapp/kit/commit/76581a3eacd15ebd1449ec3c0599ef03d2353274) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.1.4...docker/v0.2.0

## docker/v0.1.4

### Bug fixes

* realign structs([58ba675](https://github.com/getarcaneapp/kit/commit/58ba675f6a8a1b6b2eb7338a5d8ff1050620248c) by @kmendell)
* take ExecRequest and LogOptions structs([952048d](https://github.com/getarcaneapp/kit/commit/952048dc1746650df14416d014d2036f47348418) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.1.3...docker/v0.1.4

## docker/v0.1.3

### Bug fixes

* return the second log reader's error([68e8de1](https://github.com/getarcaneapp/kit/commit/68e8de1f59d130c369021ebecce7b2c9049cbb1d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.1.2...docker/v0.1.3

## docker/v0.1.2

### Bug fixes

* convert to keyed literals and drop Internal names([f4dbf02](https://github.com/getarcaneapp/kit/commit/f4dbf0275260d6343f702e5128feddc1d3c6f0a6) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.1.1...docker/v0.1.2

## docker/v0.1.1

### Bug fixes

* bump deps across all modules([bf8bf7f](https://github.com/getarcaneapp/kit/commit/bf8bf7fb40e108ff9b3bb580b4e47b3eae85da69) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/v0.1.0...docker/v0.1.1

## docker/v0.1.0

### New features

* add docker convert([f0cded7](https://github.com/getarcaneapp/kit/commit/f0cded781e43f05cfc129894e53372c6eb37d00c) by @kmendell)
* add docker module([fe017e1](https://github.com/getarcaneapp/kit/commit/fe017e1069781aa53a6a9e98bd7df13616710b15) by @kmendell)



