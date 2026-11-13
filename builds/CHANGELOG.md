## builds/v0.5.4

### Bug fixes

* bump docker/moby deps([74c7bbe](https://github.com/getarcaneapp/kit/commit/74c7bbeea90555426f17a59976d6e0ae174ccaed) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.5.3...builds/v0.5.4

## builds/v0.5.3

### Bug fixes

* skip non-regular files when staging the build context([7aa5d05](https://github.com/getarcaneapp/kit/commit/7aa5d05941a01bd4f7d8daf58d4b7bb7048645a8) by @kmendell)
* stop copying ignored context files for staged dockerfiles([e85f62a](https://github.com/getarcaneapp/kit/commit/e85f62aaa7c0d23b0f4b1cd75a0b794123276c0a) by @kmendell)
* stage read-only context directories([d4922b3](https://github.com/getarcaneapp/kit/commit/d4922b32f600fb71597497ac9bad821464d4ce4c) by @kmendell)
* drop blank platforms once before building([e480af0](https://github.com/getarcaneapp/kit/commit/e480af06833a7128eff48c149e96beb07b34f7b7) by @kmendell)
* reject git context refs that start with a dash([8e402c0](https://github.com/getarcaneapp/kit/commit/8e402c0cef5673c087f8136b119419614b765abb) by @kmendell)
* resolve lint issues in context staging and tests([269ee9b](https://github.com/getarcaneapp/kit/commit/269ee9be792c0846f361df2e5a04dc6a9cbda412) by @kmendell)
* bump deps([a7c1bfe](https://github.com/getarcaneapp/kit/commit/a7c1bfea542b3fb145814e95a2ec01de83791002) by @kmendell)

### Performance improvements

* load registry auth once per push([b3f4c22](https://github.com/getarcaneapp/kit/commit/b3f4c2206628bf54fd9d27733f37477933408b5c) by @kmendell)

### Documentation

* drop git url from the context dir description([be34b32](https://github.com/getarcaneapp/kit/commit/be34b3218cb64418b1c5028fc6915910262ddbee) by @kmendell)

### Other

* type the build provider map([e2d9fa9](https://github.com/getarcaneapp/kit/commit/e2d9fa97d5219e323eb4d637e25a302bf18c8dfc) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.5.2...builds/v0.5.3

## builds/v0.5.2

### Bug fixes

* bump golang deps([7efb0f2](https://github.com/getarcaneapp/kit/commit/7efb0f2fab12e5bcddc7b82435f0df3606700d3d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.5.1...builds/v0.5.2

## builds/v0.5.1

### Bug fixes

* bump depot-go, docker cli, buildkit and fsutil([9c86eaf](https://github.com/getarcaneapp/kit/commit/9c86eaf3cbc3d931a10835e039bcf2ed8440333d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.5.0...builds/v0.5.1

## builds/v0.5.0

### New features

* move to kit.getarcane.app/builds([bdd0da4](https://github.com/getarcaneapp/kit/commit/bdd0da41247b5a069c33056e06f908397209daa3) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.9...builds/v0.5.0

## builds/v0.4.9

### Bug fixes

* realign structs([58ba675](https://github.com/getarcaneapp/kit/commit/58ba675f6a8a1b6b2eb7338a5d8ff1050620248c) by @kmendell)
* validate required providers once in NewService([a4e6016](https://github.com/getarcaneapp/kit/commit/a4e60160d37fa2b0405338278627b6d5ba9fc92a) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.8...builds/v0.4.9

## builds/v0.4.8



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.7...builds/v0.4.8

## builds/v0.4.7

### Bug fixes

* return the progress display error from docker build streams([2ca4bf2](https://github.com/getarcaneapp/kit/commit/2ca4bf2f8376fbe93a6ec5b625f0c25a902bdd3a) by @kmendell)
* bump deps([b1c02ac](https://github.com/getarcaneapp/kit/commit/b1c02ac9e1bb12b26121768b16e9d5b651cc0ff0) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.6...builds/v0.4.7

## builds/v0.4.6

### Bug fixes

* convert to keyed literals and inline helpers([b34a3a0](https://github.com/getarcaneapp/kit/commit/b34a3a0d860fbb6a1e405acaa5a14ec410690b33) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.5...builds/v0.4.6

## builds/v0.4.5

### Bug fixes

* bump deps across all modules([bf8bf7f](https://github.com/getarcaneapp/kit/commit/bf8bf7fb40e108ff9b3bb580b4e47b3eae85da69) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.4...builds/v0.4.5

## builds/v0.4.4

### Bug fixes

* cleanup un-needed functions([0219021](https://github.com/getarcaneapp/kit/commit/021902153905003018894bc13b4cfa47491d4ec7) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.3...builds/v0.4.4

## builds/v0.4.3

### New features

* move over helpers from arcane codebase([b4272a8](https://github.com/getarcaneapp/kit/commit/b4272a870d13199eda8d13493ac86b1ad657b7f5) by @kmendell)

### Bug fixes

* update deps([03fe0c8](https://github.com/getarcaneapp/kit/commit/03fe0c8e1d6670abead50f9efd8628e9cb67592c) by @kmendell)

### Dependencies

* bump github.com/docker/cli from 29.8.1+incompatible to 29.8.2+incompatible ([#15](https://github.com/getarcaneapp/kit/pull/15) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.2...builds/v0.4.3

## builds/v0.4.2

### Bug fixes

* parse Dockerfiles, .dockerignore and build options with upstream libraries([2bd3f78](https://github.com/getarcaneapp/kit/commit/2bd3f789786118c772e222cd8f041aa1c3dae775) by @kmendell)

### Dependencies

* bump golang.org/x/text from 0.41.0 to 0.42.0 ([#6](https://github.com/getarcaneapp/kit/pull/6) by @dependabot[bot])
* bump github.com/containerd/containerd/v2 from 2.3.4 to 2.3.5 in /builds ([#4](https://github.com/getarcaneapp/kit/pull/4) by @dependabot[bot])
* bump github.com/docker/cli from 29.8.0+incompatible to 29.8.1+incompatible ([#8](https://github.com/getarcaneapp/kit/pull/8) by @dependabot[bot])
* bump github.com/containerd/containerd/v2 from 2.3.5 to 2.3.6 ([#14](https://github.com/getarcaneapp/kit/pull/14) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.1...builds/v0.4.2

## builds/v0.4.1

### Bug fixes

* bump deps([652777f](https://github.com/getarcaneapp/kit/commit/652777f47422cf07e595f5169e8479e63ce7d5bd) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/builds/v0.4.0...builds/v0.4.1

