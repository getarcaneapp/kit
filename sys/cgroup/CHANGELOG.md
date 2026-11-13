## sys/cgroup/v0.3.3

### Bug fixes

* keep v1 limits read from the controller root([b5d1c04](https://github.com/getarcaneapp/kit/commit/b5d1c04e4877a2e2979d00312db5cecde67b505d) by @kmendell)
* match v1 controllers exactly with a path per controller([377e98e](https://github.com/getarcaneapp/kit/commit/377e98eac1086c8caa05cba033519e4be5712fba) by @kmendell)
* resolve the process cgroup through mountinfo([d6733bd](https://github.com/getarcaneapp/kit/commit/d6733bdabca26b6cc0a8159d9e2190451857f6d5) by @kmendell)
* resolve lint issues in v1 cpu path fallback([bdf0cac](https://github.com/getarcaneapp/kit/commit/bdf0cac07c59a68e2e8cabf86f0959517e8cba52) by @kmendell)
* bump deps([a7c1bfe](https://github.com/getarcaneapp/kit/commit/a7c1bfea542b3fb145814e95a2ec01de83791002) by @kmendell)

### Performance improvements

* probe only virtualization instead of full host info([77abd04](https://github.com/getarcaneapp/kit/commit/77abd041a35ed83b4a7e57da8f1cbcff382f1151) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.3.2...sys/cgroup/v0.3.3

## sys/cgroup/v0.3.2

### Bug fixes

* bump golang deps([7efb0f2](https://github.com/getarcaneapp/kit/commit/7efb0f2fab12e5bcddc7b82435f0df3606700d3d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.3.1...sys/cgroup/v0.3.2

## sys/cgroup/v0.3.1

### Bug fixes

* bump gopsutil to v4.26.10([4b2aa7e](https://github.com/getarcaneapp/kit/commit/4b2aa7ede3fd48f5b7a0aae9ea064b84ef7ecf16) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.3.0...sys/cgroup/v0.3.1

## sys/cgroup/v0.3.0

### New features

* move to kit.getarcane.app/sys/cgroup([2c69e0e](https://github.com/getarcaneapp/kit/commit/2c69e0e53ce8029ecd9e1eb10878683b351c59a6) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.7...sys/cgroup/v0.3.0

## sys/cgroup/v0.2.7

### Bug fixes

* realign structs([58ba675](https://github.com/getarcaneapp/kit/commit/58ba675f6a8a1b6b2eb7338a5d8ff1050620248c) by @kmendell)
* configure Cache with a CacheConfig literal([ac19458](https://github.com/getarcaneapp/kit/commit/ac194587d08306d2d058c1a51ccc6e8d9a36a0d2) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.6...sys/cgroup/v0.2.7

## sys/cgroup/v0.2.6

### Performance improvements

* memoize container detection([f095842](https://github.com/getarcaneapp/kit/commit/f095842905b4eb0ba7b2d558b2d3ba06cd1e36b0) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.5...sys/cgroup/v0.2.6

## sys/cgroup/v0.2.5

### Bug fixes

* build limits and cache with keyed literals([d75061c](https://github.com/getarcaneapp/kit/commit/d75061c5b7ce648b0a420f38cd31c80a623c3234) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.4...sys/cgroup/v0.2.5

## sys/cgroup/v0.2.4

### New features

* move over helpers from arcane codebase([b4272a8](https://github.com/getarcaneapp/kit/commit/b4272a870d13199eda8d13493ac86b1ad657b7f5) by @kmendell)

### Bug fixes

* update deps([03fe0c8](https://github.com/getarcaneapp/kit/commit/03fe0c8e1d6670abead50f9efd8628e9cb67592c) by @kmendell)

### Dependencies

* bump golang.org/x/text from 0.41.0 to 0.42.0 ([#6](https://github.com/getarcaneapp/kit/pull/6) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.3...sys/cgroup/v0.2.4

## sys/cgroup/v0.2.3

### Bug fixes

* add missing dependency([5a4c59c](https://github.com/getarcaneapp/kit/commit/5a4c59cac1b786b8d1aaccdb565cb68b5334ce38) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.2...sys/cgroup/v0.2.3

## sys/cgroup/v0.2.2

### Bug fixes

* add guest memory accounting for Docker inside LXC([118fc28](https://github.com/getarcaneapp/kit/commit/118fc28e6ce7cb862b7074ba6e06001448bc18a1) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.1...sys/cgroup/v0.2.2

## sys/cgroup/v0.2.1

### Bug fixes

* bump deps([652777f](https://github.com/getarcaneapp/kit/commit/652777f47422cf07e595f5169e8479e63ce7d5bd) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/sys/cgroup/v0.2.0...sys/cgroup/v0.2.1

