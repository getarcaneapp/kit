## updater/v0.11.5

### Bug fixes

* preserve configured image reference when recreating containers([afa3d1b](https://github.com/getarcaneapp/kit/commit/afa3d1b7ac2927cc6b816f7afee0405b1fa7359a) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.4...updater/v0.11.5

## updater/v0.11.4

### Bug fixes

* extend self updated type with PullImageRef([e448adc](https://github.com/getarcaneapp/kit/commit/e448adc15d80402f79e7d4b2f83e52f08773ce27) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.3...updater/v0.11.4

## updater/v0.11.3

### Bug fixes

* cleanup un-needed functions([0219021](https://github.com/getarcaneapp/kit/commit/021902153905003018894bc13b4cfa47491d4ec7) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.2...updater/v0.11.3

## updater/v0.11.2

### Bug fixes

* include arcane agent containers in self lookup([3e4e23f](https://github.com/getarcaneapp/kit/commit/3e4e23f2e98d6a4bba5adb9593cc0dc6fc052dd8) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.1...updater/v0.11.2

## updater/v0.11.1

### New features

* move over helpers from arcane codebase([b4272a8](https://github.com/getarcaneapp/kit/commit/b4272a870d13199eda8d13493ac86b1ad657b7f5) by @kmendell)

### Bug fixes

* update deps([03fe0c8](https://github.com/getarcaneapp/kit/commit/03fe0c8e1d6670abead50f9efd8628e9cb67592c) by @kmendell)

### Dependencies

* bump github.com/docker/cli from 29.8.1+incompatible to 29.8.2+incompatible ([#15](https://github.com/getarcaneapp/kit/pull/15) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.0...updater/v0.11.1

## updater/v0.11.0

### Bug fixes

* take authn credentials and compare API versions with moby's package([dde730c](https://github.com/getarcaneapp/kit/commit/dde730cae1a8a737fedbf1910926983db8d9c1f7) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.10.2...updater/v0.11.0

## updater/v0.10.2

### Bug fixes

* request the scope from the registry's auth challenge ([#12](https://github.com/getarcaneapp/kit/pull/12) by @DominikZublasing)
* follow anonymous redirects when listing registry tags ([#11](https://github.com/getarcaneapp/kit/pull/11) by @GiulioSavini)

### Dependencies

* bump github.com/docker/cli from 29.8.0+incompatible to 29.8.1+incompatible ([#8](https://github.com/getarcaneapp/kit/pull/8) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.10.1...updater/v0.10.2

## updater/v0.10.1

### Bug fixes

* default an undeclared strategy to digest([778f170](https://github.com/getarcaneapp/kit/commit/778f17098e1fcad9789065f14722226142eb3aa7) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.10.0...updater/v0.10.1

## updater/v0.10.0

### New features

* let UpdateContainer override settings exclusions([91b26fc](https://github.com/getarcaneapp/kit/commit/91b26fc3698916464c9a851f1b2d43056e55f44c) by @kmendell)

### Dependencies

* bump golang.org/x/text from 0.41.0 to 0.42.0 ([#6](https://github.com/getarcaneapp/kit/pull/6) by @dependabot[bot])
* bump github.com/Masterminds/semver/v3 from 3.4.0 to 3.5.0 ([#2](https://github.com/getarcaneapp/kit/pull/2) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.9.3...updater/v0.10.0

## updater/v0.9.3

### Bug fixes

* apply pagination to tag based updates([7ce921b](https://github.com/getarcaneapp/kit/commit/7ce921bf97d933e51f763ea5ee35f5ad97c7dae2) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.9.2...updater/v0.9.3

## updater/v0.9.2

### Bug fixes

* apply pre-pulled updates and skip ineligible targets([95753a1](https://github.com/getarcaneapp/kit/commit/95753a1fe06394fd09086d2dc44f59abfe3efab4) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.9.1...updater/v0.9.2

## updater/v0.9.1

### Bug fixes

* auto detect tag updates([b4b62ba](https://github.com/getarcaneapp/kit/commit/b4b62baab863f4e2340dc4b84f37b2da9b41ba67) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.9.0...updater/v0.9.1

## updater/v0.9.0

### New features

* add normalization package([33cc521](https://github.com/getarcaneapp/kit/commit/33cc521755b5df62585e6a0f45561f031fde315d) by @kmendell)
* add tag based updates ([#1](https://github.com/getarcaneapp/kit/pull/1) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.8.1...updater/v0.9.0

## updater/v0.8.1

### Bug fixes

* bump deps([652777f](https://github.com/getarcaneapp/kit/commit/652777f47422cf07e595f5169e8479e63ce7d5bd) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.8.0...updater/v0.8.1

