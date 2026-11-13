## kit/v0.7.2

### Bug fixes

* default zero supervisor backoff fields individually([5efedab](https://github.com/getarcaneapp/kit/commit/5efedab298a69f6cc7dae57bc72f22bc6746eba5) by @kmendell)
* recover panics in background cache revalidation([a83a742](https://github.com/getarcaneapp/kit/commit/a83a74268d5ecc1fdbc4af162230b0b503f816dd) by @kmendell)
* keep cache deletes made during an in-flight load([4234cd3](https://github.com/getarcaneapp/kit/commit/4234cd32cd156b37c9420af4b6182bddd9d78ded) by @kmendell)
* keep per-entry cache ttl across revalidation([27f0e1d](https://github.com/getarcaneapp/kit/commit/27f0e1dd5b8cc0b1a9b130be7447a2fd32e43019) by @kmendell)
* sort NormalizeJSON object keys and validate input once([8d8547d](https://github.com/getarcaneapp/kit/commit/8d8547d58b58b13936c18ee0edfc891abc95bcb8) by @kmendell)
* round capacity strings up into the next unit([2dea6d6](https://github.com/getarcaneapp/kit/commit/2dea6d6aaa338ea4f091be221fc3a9d085b9a963) by @kmendell)
* apply normalization tags to fields hidden from JSON([8952103](https://github.com/getarcaneapp/kit/commit/8952103d9317a40b117fc3e70c612d13e6e5957f) by @kmendell)
* mark capture truncated when the limit is non-positive([438e1d5](https://github.com/getarcaneapp/kit/commit/438e1d5aec3ad25798e43d596275584a3b0fb1c1) by @kmendell)
* order Fingerprint map keys by type and stop at cycles([7539000](https://github.com/getarcaneapp/kit/commit/7539000abdf6faace5f4a77510953033c251804f) by @kmendell)
* resolve lint issues in bytes and cache tests([83340aa](https://github.com/getarcaneapp/kit/commit/83340aadc5014e05376d1ce8714ef4ff349700a8) by @kmendell)
* require an encryption key outside development([97391c6](https://github.com/getarcaneapp/kit/commit/97391c6c01ff61bfc3ec30d14867ce2b1c15b15e) by @kmendell)

### Performance improvements

* read cached normalization plans without locking([c93cc32](https://github.com/getarcaneapp/kit/commit/c93cc32805be7b59185d6cfda14b256e36abdd89) by @kmendell)
* hash SyncGate bodies once and drop them after Begin([a3fc68a](https://github.com/getarcaneapp/kit/commit/a3fc68a744688f0d9a13ff29560a44beb17c2d07) by @kmendell)

### Other

* build cache, capacity and length-tag values as literals([571c000](https://github.com/getarcaneapp/kit/commit/571c000c9f16591d0cdd6ee298733764255860f6) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/kit/v0.7.1...kit/v0.7.2

## kit/v0.7.1

### Bug fixes

* bump golang deps([7efb0f2](https://github.com/getarcaneapp/kit/commit/7efb0f2fab12e5bcddc7b82435f0df3606700d3d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/kit/v0.7.0...kit/v0.7.1

## kit/v0.7.0

### New features

* add minimal cache package([3d8d155](https://github.com/getarcaneapp/kit/commit/3d8d15507a540f1edb6ef02d010b50eea0f5bbb1) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/kit/v0.6.0...kit/v0.7.0

## kit/v0.6.0

### New features

* add concurrency package, LookupEnvOrFile, fs perms, and tag metadata helpers([c6cac51](https://github.com/getarcaneapp/kit/commit/c6cac5121d9c50fe11e5a4e21e616724c25aebca) by @Kyle Mendell)

### Bug fixes

* match registry host normalization to arcane rules([28d0905](https://github.com/getarcaneapp/kit/commit/28d0905c3efea34861678b102013d2f44c6871d5) by @Kyle Mendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/kit/v0.5.1...kit/v0.6.0

## kit/v0.5.1

### Bug fixes

* restore key derivation context([a5d3081](https://github.com/getarcaneapp/kit/commit/a5d30816c484f1a56b926e78ab2a290731fe7f25) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/kit/v0.5.0...kit/v0.5.1

## kit/v0.5.0

### New features

* move kit module to kit.getarcane.app([d500cd3](https://github.com/getarcaneapp/kit/commit/d500cd381427e4bfda8808a3849c6aa752945311) by @Kyle Mendell)



## v0.4.4

### Bug fixes

* realign structs([58ba675](https://github.com/getarcaneapp/kit/commit/58ba675f6a8a1b6b2eb7338a5d8ff1050620248c) by @kmendell)
* replace normalization.Text bool flags with TextOptions([3e30272](https://github.com/getarcaneapp/kit/commit/3e3027269858658b8c7df2b1309e7d1d6226acc3) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.4.3...v0.4.4

## v0.4.3



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.4.2...v0.4.3

## v0.4.2

### Bug fixes

* build plans and helpers with keyed literals([92f3085](https://github.com/getarcaneapp/kit/commit/92f30851ee30ac052643d0d969dc8a2630acf421) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.4.1...v0.4.2

## v0.4.1

### Bug fixes

* bump deps across all modules([bf8bf7f](https://github.com/getarcaneapp/kit/commit/bf8bf7fb40e108ff9b3bb580b4e47b3eae85da69) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.4.0...v0.4.1

## v0.4.0

### New features

* add docker module([fe017e1](https://github.com/getarcaneapp/kit/commit/fe017e1069781aa53a6a9e98bd7df13616710b15) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.3.3...v0.4.0

## v0.3.3

### Bug fixes

* update deps([03fe0c8](https://github.com/getarcaneapp/kit/commit/03fe0c8e1d6670abead50f9efd8628e9cb67592c) by @kmendell)
* include arcane agent containers in self lookup([3e4e23f](https://github.com/getarcaneapp/kit/commit/3e4e23f2e98d6a4bba5adb9593cc0dc6fc052dd8) by @kmendell)
* cleanup un-needed functions([0219021](https://github.com/getarcaneapp/kit/commit/021902153905003018894bc13b4cfa47491d4ec7) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.3.2...v0.3.3

## v0.3.2

### Bug fixes

* add FromPtr function([6c850e6](https://github.com/getarcaneapp/kit/commit/6c850e62bcaff14689063043cb84dc1e08d920b8) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.3.1...v0.3.2

## v0.3.1

### Bug fixes

* move more helpers from arcane([1fa3b5f](https://github.com/getarcaneapp/kit/commit/1fa3b5f6cbaacd949dde62de536da5bcfa639266) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.3.0...v0.3.1

## v0.3.0

### New features

* move over helpers from arcane codebase([b4272a8](https://github.com/getarcaneapp/kit/commit/b4272a870d13199eda8d13493ac86b1ad657b7f5) by @kmendell)

### Bug fixes

* apply pagination to tag based updates([7ce921b](https://github.com/getarcaneapp/kit/commit/7ce921bf97d933e51f763ea5ee35f5ad97c7dae2) by @kmendell)
* default an undeclared strategy to digest([778f170](https://github.com/getarcaneapp/kit/commit/778f17098e1fcad9789065f14722226142eb3aa7) by @kmendell)

### Dependencies

* bump golang.org/x/text from 0.41.0 to 0.42.0 ([#6](https://github.com/getarcaneapp/kit/pull/6) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.2.0...v0.3.0

## v0.2.0

### New features

* add normalization package([33cc521](https://github.com/getarcaneapp/kit/commit/33cc521755b5df62585e6a0f45561f031fde315d) by @kmendell)

### Bug fixes

* add guest memory accounting for Docker inside LXC([118fc28](https://github.com/getarcaneapp/kit/commit/118fc28e6ce7cb862b7074ba6e06001448bc18a1) by @kmendell)
* add missing dependency([5a4c59c](https://github.com/getarcaneapp/kit/commit/5a4c59cac1b786b8d1aaccdb565cb68b5334ce38) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.1.1...v0.2.0

## v0.1.1

### Bug fixes

* match Docker CLI memory accounting for cgroup v1 containers([b14948c](https://github.com/getarcaneapp/kit/commit/b14948cb1b944c25038724d1b8fe6a3eb47470b8) by @kmendell)
* bump deps([652777f](https://github.com/getarcaneapp/kit/commit/652777f47422cf07e595f5169e8479e63ce7d5bd) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/v0.1.0...v0.1.1

