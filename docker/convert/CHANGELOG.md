## docker/convert/v0.4.3

### Bug fixes

* honour explicit values on long boolean flags([8cdddb9](https://github.com/getarcaneapp/kit/commit/8cdddb9736b8b197cf2a669d330849c80eeb370e) by @kmendell)
* stop unknown flags from consuming the image token([8736885](https://github.com/getarcaneapp/kit/commit/87368850d5f7ac0482de4b4f53185ba0c706987c) by @kmendell)
* map hostname, ip and network aliases([3466ec2](https://github.com/getarcaneapp/kit/commit/3466ec2d87c34746eaa46f15e2618c4ec6213e12) by @kmendell)
* give stop-timeout a seconds unit([cff1f53](https://github.com/getarcaneapp/kit/commit/cff1f5317c0c34913c948ee025bcc5aae1383d7f) by @kmendell)
* keep existing compose structure when merging([9c18a3b](https://github.com/getarcaneapp/kit/commit/9c18a3bac5ff6f2d0bac6dd4234548030f425324) by @kmendell)
* convert --mount to long-syntax volumes([07a7cf9](https://github.com/getarcaneapp/kit/commit/07a7cf91dc67a92bb366cdeb0e27761cbbb299cf) by @kmendell)
* convert --gpus options to device requests([51d5d94](https://github.com/getarcaneapp/kit/commit/51d5d949a335001c24beb8702e70a73e46ef29bf) by @kmendell)
* stop a bare dash token from panicking the parser([484973c](https://github.com/getarcaneapp/kit/commit/484973c91d25d036d01243ae86a7954de3c29925) by @kmendell)
* resolve lint issues in gpu parsing and tests([a57d714](https://github.com/getarcaneapp/kit/commit/a57d71428b53758b6a339b1ce23f567bc9b934ed) by @kmendell)
* bump deps([a7c1bfe](https://github.com/getarcaneapp/kit/commit/a7c1bfea542b3fb145814e95a2ec01de83791002) by @kmendell)

### Other

* assert the result of parsing a bare dash([3e65565](https://github.com/getarcaneapp/kit/commit/3e65565134d52a9dc40313ec3a8fb75786a70de8) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.4.2...docker/convert/v0.4.3

## docker/convert/v0.4.2

### Bug fixes

* bump golang deps([7efb0f2](https://github.com/getarcaneapp/kit/commit/7efb0f2fab12e5bcddc7b82435f0df3606700d3d) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.4.1...docker/convert/v0.4.2

## docker/convert/v0.4.1

### Bug fixes

* bump go.yaml.in/yaml/v4 to v4.0.0([2333958](https://github.com/getarcaneapp/kit/commit/2333958e9c32dd7397508bcd2eaf980ecb1ff2ac) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.4.0...docker/convert/v0.4.1

## docker/convert/v0.4.0

### New features

* move to kit.getarcane.app/docker/convert([c79df79](https://github.com/getarcaneapp/kit/commit/c79df79aabeda6ea9561b3883051532696f1a9bd) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.8...docker/convert/v0.4.0

## docker/convert/v0.3.8

### Bug fixes

* drop unused ParseOptions and dedupe RenderWarnings([a098b4a](https://github.com/getarcaneapp/kit/commit/a098b4a461ce8789f5b12487d4c952c2be7e4070) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.7...docker/convert/v0.3.8

## docker/convert/v0.3.7

### Bug fixes

* bump deps([b1c02ac](https://github.com/getarcaneapp/kit/commit/b1c02ac9e1bb12b26121768b16e9d5b651cc0ff0) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.6...docker/convert/v0.3.7

## docker/convert/v0.3.6

### Bug fixes

* build documents and nodes with literals([bd68ab7](https://github.com/getarcaneapp/kit/commit/bd68ab75a8e9748d348001852f54615f18d1908a) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.5...docker/convert/v0.3.6

## docker/convert/v0.3.5

### Bug fixes

* bump deps across all modules([bf8bf7f](https://github.com/getarcaneapp/kit/commit/bf8bf7fb40e108ff9b3bb580b4e47b3eae85da69) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.4...docker/convert/v0.3.5

## docker/convert/v0.3.4

### Bug fixes

* cleanup un-needed functions([0219021](https://github.com/getarcaneapp/kit/commit/021902153905003018894bc13b4cfa47491d4ec7) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.3...docker/convert/v0.3.4

## docker/convert/v0.3.3

### New features

* move over helpers from arcane codebase([b4272a8](https://github.com/getarcaneapp/kit/commit/b4272a870d13199eda8d13493ac86b1ad657b7f5) by @kmendell)

### Bug fixes

* update deps([03fe0c8](https://github.com/getarcaneapp/kit/commit/03fe0c8e1d6670abead50f9efd8628e9cb67592c) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.2...docker/convert/v0.3.3

## docker/convert/v0.3.2

### Bug fixes

* split commands with go-shellwords and emit command as a list([f77543d](https://github.com/getarcaneapp/kit/commit/f77543d1de5852ef23b6821186914a3b448c34da) by @kmendell)

### Dependencies

* bump golang.org/x/text from 0.41.0 to 0.42.0 ([#6](https://github.com/getarcaneapp/kit/pull/6) by @dependabot[bot])
* bump github.com/mattn/go-shellwords from 1.0.14 to 1.0.15 in /docker/convert ([#7](https://github.com/getarcaneapp/kit/pull/7) by @dependabot[bot])



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.1...docker/convert/v0.3.2

## docker/convert/v0.3.1

### Bug fixes

* bump deps([652777f](https://github.com/getarcaneapp/kit/commit/652777f47422cf07e595f5169e8479e63ce7d5bd) by @kmendell)



**Full Changelog**: https://github.com/getarcaneapp/kit/compare/docker/convert/v0.3.0...docker/convert/v0.3.1

