## acfs/v0.10.2

### New features

* bound ListEach memory by an entry limit ([#29](https://github.com/getarcaneapp/kit/pull/29) by @neurekadev)

### Bug fixes

* strip setuid and setgid from copied files([8f0dd88](https://github.com/getarcaneapp/kit/commit/8f0dd881b7759465cb520d1ba319b8af45d134bb) by @kmendell)
* walk copy sources by descriptor to stay inside the root([c54f48c](https://github.com/getarcaneapp/kit/commit/c54f48c98fb624efb96611e362d462692116910a) by @kmendell)
* check file type before opening read and copy targets([085bd83](https://github.com/getarcaneapp/kit/commit/085bd838646a5bcbd0bd5aec29b44e419afecbad) by @kmendell)
* report renames onto directories as already existing([6b0fec5](https://github.com/getarcaneapp/kit/commit/6b0fec5a17be0a5b02b9745aef4868ea0b7ab93a) by @kmendell)
* resolve lint issues in copy and walk([6d655af](https://github.com/getarcaneapp/kit/commit/6d655af47d9eaf41c417c0b6fde424541888c645) by @kmendell)

### Performance improvements

* stream copied files and compare mirrors in chunks([6154107](https://github.com/getarcaneapp/kit/commit/615410741a7745d63d27132f899288c3e85dea7a) by @kmendell)
* probe walk depth boundary with a small directory read([9ce8364](https://github.com/getarcaneapp/kit/commit/9ce83646eb7be5c6f50655722cc94df89d1f8677) by @kmendell)
* size list reads so a limited list holds at most 2x names([a09cd91](https://github.com/getarcaneapp/kit/commit/a09cd91c431750d5679fc8a4f0ad968938982b86) by @kmendell)
* compare in-place writes by size, then in chunks([d385e46](https://github.com/getarcaneapp/kit/commit/d385e46126c802b474317c5628da4fb2f4f3e021) by @kmendell)
* stream list output to stdout instead of a temp file([dce87b1](https://github.com/getarcaneapp/kit/commit/dce87b1424d25fe098a3ab35224e1ec9adf14ff8) by @kmendell)

### Other

* build literals one field per line and once([d30afb1](https://github.com/getarcaneapp/kit/commit/d30afb1fd11cb23989a93ad60060340c88b3e03a) by @kmendell)
* drop a fifo copy check that never failed before the fix([f5edb1f](https://github.com/getarcaneapp/kit/commit/f5edb1faf1a5f2bf5db9c4db4672f38835593ffb) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.10.1...acfs/v0.10.2

## acfs/v0.10.1

### Bug fixes

* bump golang deps([7efb0f2](https://github.com/getarcaneapp/kit/commit/7efb0f2fab12e5bcddc7b82435f0df3606700d3d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.10.0...acfs/v0.10.1

## acfs/v0.10.0

### New features

* add missing file system apis([153e344](https://github.com/getarcaneapp/kit/commit/153e344bdc9bd054e0cb7ba43280c21c1315887c) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.9.0...acfs/v0.10.0

## acfs/v0.9.0

### New features

* add --limit to list([56634e5](https://github.com/getarcaneapp/kit/commit/56634e5673bb766c5187e415fc127b0c8a4d73d5) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.8.0...acfs/v0.9.0

## acfs/v0.8.0

### New features

* race-safe root operations and a Host API([ed0b005](https://github.com/getarcaneapp/kit/commit/ed0b005dd7eefb3e0d99417ce888a5cf96a8951e) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.7.0...acfs/v0.8.0

## acfs/v0.7.0

### New features

* move to kit.getarcane.app/acfs([7fa8a31](https://github.com/getarcaneapp/kit/commit/7fa8a31faa3e5c60bb1671a0cd8fd30e594f88b1) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.6.4...acfs/v0.7.0

## acfs/v0.6.4

### Bug fixes

* realign structs([58ba675](https://github.com/getarcaneapp/kit/commit/58ba675f6a8a1b6b2eb7338a5d8ff1050620248c) by @kmendell)
* make operations methods on a Root value type([c6fa5b7](https://github.com/getarcaneapp/kit/commit/c6fa5b784c879db379e75746d67be6363b73fa22) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.6.3...acfs/v0.6.4

## acfs/v0.6.3

### Bug fixes

* convert to composite literal layout([5cee732](https://github.com/getarcaneapp/kit/commit/5cee7328dc01a756f609a64474dcce6f1a09e26f) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.6.2...acfs/v0.6.3

## acfs/v0.6.2

### New features

* move over helpers from arcane codebase([b4272a8](https://github.com/getarcaneapp/kit/commit/b4272a870d13199eda8d13493ac86b1ad657b7f5) by @kmendell)

### Bug fixes

* move more helpers from arcane([1fa3b5f](https://github.com/getarcaneapp/kit/commit/1fa3b5f6cbaacd949dde62de536da5bcfa639266) by @kmendell)
* update deps([03fe0c8](https://github.com/getarcaneapp/kit/commit/03fe0c8e1d6670abead50f9efd8628e9cb67592c) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.6.1...acfs/v0.6.2

## acfs/v0.6.1

### Bug fixes

* use filepath.IsLocal for outside-root checks([2451fd8](https://github.com/getarcaneapp/kit/commit/2451fd80986159da6a1f48fdafda2fa86a3a3799) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.6.0...acfs/v0.6.1

## acfs/v0.6.0

### New features

* enhance Write functionality with in-place options and improved symlink handling([f9bdb89](https://github.com/getarcaneapp/kit/commit/f9bdb89c71ff5cbb65646e8f28f9d3221190a270) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.5.1...acfs/v0.6.0

## acfs/v0.5.1



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/acfs/v0.5.0...acfs/v0.5.1

