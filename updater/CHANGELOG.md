## updater/v0.12.3

### Bug fixes

* fail pulls on daemon-reported stream errors([54a7db4](https://github.com/getarcaneapp/kit/commit/54a7db4c46ec672e8b3b7db48a7d057ad3a97eba) by @kmendell)
* finish recreate and rollback after caller cancel([ea04fd3](https://github.com/getarcaneapp/kit/commit/ea04fd33b08b7ec7efdcf773587596d15572d645) by @kmendell)
* report implicit compose restarts as restarted([0014367](https://github.com/getarcaneapp/kit/commit/001436716f574d7307f4d025f70f10f77342e56a) by @kmendell)
* skip containers another update already holds([00abe70](https://github.com/getarcaneapp/kit/commit/00abe705658ed117a8138f7f63b0b5c507fdada9) by @kmendell)
* verify compose services after a digest update([b2bf6f2](https://github.com/getarcaneapp/kit/commit/b2bf6f2cdd65db079a8249b23245d63c45a5d03c) by @kmendell)
* end compose flags before label-sourced service names([bce92ce](https://github.com/getarcaneapp/kit/commit/bce92ce24f0deafd65b7731d00540d1685e44beb) by @kmendell)
* resolve lint issues from the review fixes([39802c8](https://github.com/getarcaneapp/kit/commit/39802c8212bb20384db274e08788a4e3b26e1faf) by @kmendell)
* keep the configured image ref when rolling back from an image id([eb143ee](https://github.com/getarcaneapp/kit/commit/eb143ee3da9eaa723d5c8dfba2308c9562ea5f23) by @kmendell)
* bump deps([a7c1bfe](https://github.com/getarcaneapp/kit/commit/a7c1bfea542b3fb145814e95a2ec01de83791002) by @kmendell)

### Performance improvements

* inspect and load exclusions once per targeted run([ef6390a](https://github.com/getarcaneapp/kit/commit/ef6390af1fd5df091505da3e89d578ab3149537b) by @kmendell)

### Other

* drop redundant nil check before PreserveConfiguredRef([47310dc](https://github.com/getarcaneapp/kit/commit/47310dcbbdb6823130b5b9f3908de0a58ca85f5f) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.12.2...updater/v0.12.3

## updater/v0.12.2

### Bug fixes

* tidy go.mod([721d71f](https://github.com/getarcaneapp/kit/commit/721d71f023ceaf62c9d37e364d34336da85ab9c5) by @kmendell)
* bump golang deps([7efb0f2](https://github.com/getarcaneapp/kit/commit/7efb0f2fab12e5bcddc7b82435f0df3606700d3d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.12.1...updater/v0.12.2

## updater/v0.12.1

### Bug fixes

* bump kit to v0.6.0 and merge replace directives([5213156](https://github.com/getarcaneapp/kit/commit/5213156aca57ca1fb76ed97b28d83751f406c0aa) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.12.0...updater/v0.12.1

## updater/v0.12.0

### New features

* move to kit.getarcane.app/updater([3c904f8](https://github.com/getarcaneapp/kit/commit/3c904f8f4140e4a8d224675bbd35053b07106363) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.14...updater/v0.12.0

## updater/v0.11.14

### Bug fixes

* realign structs([58ba675](https://github.com/getarcaneapp/kit/commit/58ba675f6a8a1b6b2eb7338a5d8ff1050620248c) by @kmendell)
* take request structs and build Config in one literal([30e97cb](https://github.com/getarcaneapp/kit/commit/30e97cbbcee5ca4140b07cf56385641ed000d3b0) by @kmendell)
* use CreateRequest; align exclusion test with batch opt-out bypass([a0edbaa](https://github.com/getarcaneapp/kit/commit/a0edbaa2fbd6ded5ba904a2da182ca2f7d5bc9a7) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.13...updater/v0.11.14

## updater/v0.11.13

### Bug fixes

* let user-requested batch runs skip opt-outs; add unmanaged guard([428fe53](https://github.com/getarcaneapp/kit/commit/428fe53c3aea2679218fe7ecb694c8ac2800967d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.12...updater/v0.11.13

## updater/v0.11.12

### Bug fixes

* bump deps([b1c02ac](https://github.com/getarcaneapp/kit/commit/b1c02ac9e1bb12b26121768b16e9d5b651cc0ff0) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.11...updater/v0.11.12

## updater/v0.11.11

### Bug fixes

* pass the container id when preparing the recreate host config([395894f](https://github.com/getarcaneapp/kit/commit/395894ff3461bc635f7ce6bc5ae32f035dc1e830) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.10...updater/v0.11.11

## updater/v0.11.10

### Bug fixes

* keep the original container until its replacement starts([167193d](https://github.com/getarcaneapp/kit/commit/167193d1f8a4dfcf87027c6f701a6f714c83f6e5) by @Kyle Mendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.9...updater/v0.11.10

## updater/v0.11.9

### Bug fixes

* pull and recreate targeted updates one dependency unit at a time([442d4fa](https://github.com/getarcaneapp/kit/commit/442d4fa209affd4dbb76b32fce4535f3c553030a) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.8...updater/v0.11.9

## updater/v0.11.8

### Bug fixes

* build results and plans with keyed literals([c2de58b](https://github.com/getarcaneapp/kit/commit/c2de58b55de32a916d3c90217a41ac24b03700d3) by @kmendell)
* rename label policy merge test after inlining([508fc1d](https://github.com/getarcaneapp/kit/commit/508fc1d4adf78c0412855d3dae79bf1e4022dc9b) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.7...updater/v0.11.8

## updater/v0.11.7

### Bug fixes

* bump deps across all modules([bf8bf7f](https://github.com/getarcaneapp/kit/commit/bf8bf7fb40e108ff9b3bb580b4e47b3eae85da69) by @kmendell)
* let manual container updates bypass the updater opt-out label([d63b704](https://github.com/getarcaneapp/kit/commit/d63b7043b010c4a0e0537b34d87d1a2fb0c4ac18) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.6...updater/v0.11.7

## updater/v0.11.6

### Bug fixes

* recreate containers per docker and compose spec ([#18](https://github.com/getarcaneapp/kit/pull/18) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/updater/v0.11.5...updater/v0.11.6

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

