# Changelog

## [0.1.12](https://github.com/Geogboe/boxy/compare/v0.1.11...v0.1.12) (2026-03-31)


### Features

* **cli:** use api for sandbox commands ([66e195e](https://github.com/Geogboe/boxy/commit/66e195efafb9783789192091ca5068dad3f7661d))

## [0.1.11](https://github.com/Geogboe/boxy/compare/v0.1.10...v0.1.11) (2026-03-27)


### Features

* add devcontainer config ([1772b00](https://github.com/Geogboe/boxy/commit/1772b00183ce702b835082ae75420ac4b55724e5))


### Bug Fixes

* remove roadmap ([59af64c](https://github.com/Geogboe/boxy/commit/59af64c7eaf1fba1a0ba6506b52168a9ddf975b4))
* resolve pre-existing golangci-lint failures ([222e26b](https://github.com/Geogboe/boxy/commit/222e26b2a3684594405c35e2bd757a6721c2de4c)), closes [#59](https://github.com/Geogboe/boxy/issues/59)

## [0.1.10](https://github.com/Geogboe/boxy/compare/v0.1.9...v0.1.10) (2026-03-26)


### Features

* add agentsdk with Agent interface and EmbeddedAgent ([76613c0](https://github.com/Geogboe/boxy/commit/76613c0ef8025f21000abb35baa0ae9113da917b))
* add comprehensive release automation pipeline ([f84cc7d](https://github.com/Geogboe/boxy/commit/f84cc7d91bb00a7be8d59e12bcdb601259d0b0b5))
* add comprehensive sandbox manager integration tests ([c57ab37](https://github.com/Geogboe/boxy/commit/c57ab37d90bdd5694d5f54a786102c6ff9912707))
* add configuration management and CLI foundation ([16aca4d](https://github.com/Geogboe/boxy/commit/16aca4de1c57371c79adbb64f67017977070ebe1))
* add devbox reference provider (in-memory CRUD driver) ([2450419](https://github.com/Geogboe/boxy/commit/2450419920c2554217bbf4d1df4f25192a71e55f))
* add docker pool and lab configuration examples ([1f5a0ac](https://github.com/Geogboe/boxy/commit/1f5a0ac37610cec6d64798ab47b1361516c92f45))
* add example tasks for building and running examples ([950c44c](https://github.com/Geogboe/boxy/commit/950c44c49ff530cbd74a5b49ac6357352c9e4f79))
* add GitHub issue templates and sample issues for v1 tracking ([931c314](https://github.com/Geogboe/boxy/commit/931c314efc8ffaf4445b8c822e080017fc51b0e0))
* add GitHub issue templates and sample issues for v1 tracking ([a3aa3e1](https://github.com/Geogboe/boxy/commit/a3aa3e10628771296cdb642a905b9f4d19d8d5d2))
* add HTTP API server with sandbox management ([501ec8e](https://github.com/Geogboe/boxy/commit/501ec8e316e10f1a353840d120b4b9aaa7d51218))
* add Hyper-V local example with embedded agent configuration ([e13a88a](https://github.com/Geogboe/boxy/commit/e13a88aeeab050d4ff580ca2500aa2e2ab0a551d))
* add Hyper-V stub provider implementation ([ce8dcb8](https://github.com/Geogboe/boxy/commit/ce8dcb8a9c6544b8306f7e5c27d7a04524dc12f8))
* add JSON persistence, state machine, and CLI to devboxes provider ([44a21b3](https://github.com/Geogboe/boxy/commit/44a21b3687dd06459aa9816c3f0ef0e73da058c5))
* add JSON Schema for boxy.yaml with editor support ([f04f778](https://github.com/Geogboe/boxy/commit/f04f7789c6873f05b90bb5bcf339413cc5caec8c))
* add JSON Schema for boxy.yaml with editor support ([0d043c0](https://github.com/Geogboe/boxy/commit/0d043c02274964893b2f8f84770dda6321721030))
* add lint-enforcer and go-dev agents for code quality management ([5b5c56b](https://github.com/Geogboe/boxy/commit/5b5c56b6d266d448ccbecc3125daea95882f1483))
* add new documentation prompts and update existing files ([fd97361](https://github.com/Geogboe/boxy/commit/fd9736125c2e6024a10d0bb7f1491a1f7500ea5b))
* add pool and sandbox management features ([17b5388](https://github.com/Geogboe/boxy/commit/17b53887c393ed5d1d8233557d9af3c5fcb43363))
* add release installer scripts ([#48](https://github.com/Geogboe/boxy/issues/48)) ([2bc936d](https://github.com/Geogboe/boxy/commit/2bc936de854a006f62c387bf625b782e69254839))
* add resource profiles to devboxes provider ([5f1db4c](https://github.com/Geogboe/boxy/commit/5f1db4c3ec06b3b3459292defded839d825f5921))
* **agent:** add reusable agent runner scaffold ([c29a87c](https://github.com/Geogboe/boxy/commit/c29a87caf5c5406bbcdeda89423b286a9fb93281))
* allocation hooks, env file output, and spinner UX ([#45](https://github.com/Geogboe/boxy/issues/45)) ([26054d8](https://github.com/Geogboe/boxy/commit/26054d881c710a6d5fe9216196d03216186b99ba))
* CLI UX overhaul — wireframe, init, status, serve banner ([#41](https://github.com/Geogboe/boxy/issues/41)) ([106abe8](https://github.com/Geogboe/boxy/commit/106abe8d4d44b4b155ec9189d435865db92c33c7))
* **cli:** add minimal boxy serve ([bb17ac3](https://github.com/Geogboe/boxy/commit/bb17ac3f9fd05b8f9021442c1a69619f7272f9e2))
* **cli:** add signal-aware contexts and timeouts for better UX ([3c08812](https://github.com/Geogboe/boxy/commit/3c08812db0a6f8a2a8b0c1e38a5479be0239a86c))
* design distributed agent architecture for remote provider orchestration ([08f8ed3](https://github.com/Geogboe/boxy/commit/08f8ed3cff6242c1605e7e1bc5bd207646c0f5bb))
* enhance agent and sandbox management with new runtime and storage handling ([24c9264](https://github.com/Geogboe/boxy/commit/24c92647b4c9e0b4697e6c509fa9164eacecb4ce))
* enhance documentation and configuration for resource pooling ([4f8ef64](https://github.com/Geogboe/boxy/commit/4f8ef6426ea507c82568ab375d3b6afe1f030631))
* enhance scratch provider with connect script and fix allocation chain ([db2a26d](https://github.com/Geogboe/boxy/commit/db2a26d7f050729d96b15544492bd98daf41ab1d))
* **hooks:** add standalone hook runner package ([183d3e9](https://github.com/Geogboe/boxy/commit/183d3e945e3204c081464f737b5d834f6bcdbf8e))
* human-friendly sandbox CLI output ([#43](https://github.com/Geogboe/boxy/issues/43)) ([bc2005f](https://github.com/Geogboe/boxy/commit/bc2005f5dafc6cebbb43e3d6a4adeff9639bc436))
* Hyper-V provider, pkg/vmsdk, pkg/psdirect, Docker SDK migration ([#46](https://github.com/Geogboe/boxy/issues/46)) ([7d5561d](https://github.com/Geogboe/boxy/commit/7d5561d75b7de81b0b37390358584c3ba5f9457a))
* implement allocator for pool-sandbox orchestration ([cae46d3](https://github.com/Geogboe/boxy/commit/cae46d353371342fc90bcecfef22a250077153a2))
* implement async sandbox allocation with status tracking ([12b43ff](https://github.com/Geogboe/boxy/commit/12b43ff6be248ddc9c2f83d463b8cc13af21f154))
* implement complete CLI with serve, pool, and sandbox commands ([8e594f2](https://github.com/Geogboe/boxy/commit/8e594f26aefee28d8d2b5b1dca21961af2ba9b30))
* implement complete Hyper-V provider with PowerShell integration ([5605ccd](https://github.com/Geogboe/boxy/commit/5605ccdefabc0910711dcf7406d4247abc3be0c7))
* implement core domain models and Docker provider ([74423d7](https://github.com/Geogboe/boxy/commit/74423d7e41bb95685dff1ee85244895e7fb4df2c))
* implement distributed agent architecture (90% complete) ([7ca3428](https://github.com/Geogboe/boxy/commit/7ca3428557807c3fb78094ce1239dca4f49667b5))
* implement hook execution framework ([4810385](https://github.com/Geogboe/boxy/commit/48103850ad28cf98543e78e7fb8f2fd76d7622ed))
* implement pool manager with warm pool maintenance ([3dde05f](https://github.com/Geogboe/boxy/commit/3dde05fc52238dcc9feb0bdd51bfc0893fd1c155))
* implement sandbox manager with lifecycle orchestration ([424ace2](https://github.com/Geogboe/boxy/commit/424ace23ec745cec1ba3d49bd85f2ef52a7beef4))
* improve sandbox CLI output with clear usage instructions ([b5087af](https://github.com/Geogboe/boxy/commit/b5087afa8eed0a8622307903eade2c6224475dc5))
* **install:** update install scripts for GoReleaser archive format ([e52631b](https://github.com/Geogboe/boxy/commit/e52631b12ad1f3d30cee6e4dd3af15ca67fc7326))
* integrate hook execution into pool manager ([f9365b7](https://github.com/Geogboe/boxy/commit/f9365b7360eeeb7c10bcd4e831f8d0fa6626c4c6))
* introduce scratch provider architecture for lightweight workspaces ([9ee60cc](https://github.com/Geogboe/boxy/commit/9ee60cce42d1e84f518a9bfc5caf7afa2037a981))
* make Boxy ready for personal use with security & stability improvements ([aec0919](https://github.com/Geogboe/boxy/commit/aec0919616511cec223ec1a0d9d4cf2146d70718))
* make sandbox create work with examples ([a673818](https://github.com/Geogboe/boxy/commit/a673818895fc5c144f3e3d35ef34ec03ce1b02d3))
* merge feat/cli — CLI restructure with config, sandbox, and debug provider commands ([4e1c3d3](https://github.com/Geogboe/boxy/commit/4e1c3d3847df46cb90dcd8008d6d58a43e0a7d42))
* **model:** add resource profiles and pool constraints ([aa0a6d5](https://github.com/Geogboe/boxy/commit/aa0a6d521e6580c96e1ae1d685e3b897e3053a3c))
* **pkg:** add resourcespec, resourcepool, and providersdk scaffolds ([157712a](https://github.com/Geogboe/boxy/commit/157712ae86b50cc29b0a685bde794e66d367f736))
* **policycontroller:** add generic observe-decide-act loop ([70a3ee7](https://github.com/Geogboe/boxy/commit/70a3ee77f2f46a010361ae749aedbb675210a91b))
* **providers:** add reusable docker and hyperv driver scaffolds ([fb5e007](https://github.com/Geogboe/boxy/commit/fb5e00757071a54038264b3878f4bbb2f8c993a9))
* **providersdk:** add ExecSpec and process provider ([f12493c](https://github.com/Geogboe/boxy/commit/f12493c08d0bddea28a52c7bd00f470000e3cb8c))
* Refactor provider registration and service startup ([95963df](https://github.com/Geogboe/boxy/commit/95963dfc5e6f29988f38a5c40fc35d07a0e1b6a6))
* refine distributed architecture with token-based registration and resource interaction ([6de6e59](https://github.com/Geogboe/boxy/commit/6de6e59aac5d96eacd25d045e6fe9c1f79ef2a45))
* restructure CLI with config, sandbox, and debug provider commands ([ebb5370](https://github.com/Geogboe/boxy/commit/ebb5370108df83775304c27874e014683403a1cc))
* update .gitignore and enhance README and ROADMAP documentation ([b8b1d8f](https://github.com/Geogboe/boxy/commit/b8b1d8f20010541d7c298e1d42d62b6bf5e4600d))
* update Docker pool examples and remove deprecated configurations ([9716f51](https://github.com/Geogboe/boxy/commit/9716f513b0cce59fa27ef40b22741cb9abf18b9c))
* update pool configurations to use 'docker' type ([512fb2d](https://github.com/Geogboe/boxy/commit/512fb2d4bb50c65dc2fc933d115cb01b30256253))


### Bug Fixes

* add errcheck exclusion presets for v2 lint compatibility ([963b232](https://github.com/Geogboe/boxy/commit/963b23206deaecf68469aa716159af9cb07b1b15))
* add legacy exclusion preset to restore v1 lint behavior ([6809233](https://github.com/Geogboe/boxy/commit/680923322cbc549ff1c9839f61407b3e868cf41c))
* clean up cli lint issues ([f1627d1](https://github.com/Geogboe/boxy/commit/f1627d170b042e58a7f997bd68790cc4500dc27b))
* correct compilation errors and build successfully ([6b5bdac](https://github.com/Geogboe/boxy/commit/6b5bdac6fec30a7199f9162f101e1a4552b8a3d7))
* correct mock provider resource lookup in Execute/Update ([3d756e7](https://github.com/Geogboe/boxy/commit/3d756e76a75c7cc8c8a15716e7f674c2354cfeab))
* enforce one driver per provider type per agent ([0814a4b](https://github.com/Geogboe/boxy/commit/0814a4b3dd3d64b1fae4f7f047b887ebf87e9fea))
* exclude errcheck from test files in lint config ([36445c5](https://github.com/Geogboe/boxy/commit/36445c5c4666ff3bda6f592f03c63ca8138d2b4f))
* exclude errcheck from test files in lint config ([06bf4c2](https://github.com/Geogboe/boxy/commit/06bf4c214692e4785008b85718b5acd166a1b5e9))
* harden installer release compatibility ([996a6b6](https://github.com/Geogboe/boxy/commit/996a6b6db97ebf83c0f1c50c4ec8aead10d6e88d))
* harden release installers ([ca8fac1](https://github.com/Geogboe/boxy/commit/ca8fac19a6295c063ffa59406e48fec1a551d641))
* **hyperv:** implement comprehensive PowerShell injection prevention and input validation ([a58f3ba](https://github.com/Geogboe/boxy/commit/a58f3ba5c39e0930e1f32e1d6b4c4bd35b28a956))
* **hyperv:** prevent ScriptBlock breakout attacks in Exec method with ArgumentList ([cdf2f13](https://github.com/Geogboe/boxy/commit/cdf2f137d2422b3d9d40f5d2d0a1ddf2e960107b))
* **install:** correct ARM detection display and improve error handling ([58d76f1](https://github.com/Geogboe/boxy/commit/58d76f122db0e5cf13ff12d63cd828b3288e09b0))
* **pool:** prevent goroutine leaks with async WaitGroup tracking ([943e1e2](https://github.com/Geogboe/boxy/commit/943e1e20b2c4875e377b92432637d10beb0af2d5))
* resolve all integration test issues and race conditions ([a1bf9bc](https://github.com/Geogboe/boxy/commit/a1bf9bcb4ac8037e2510f2960c48265dbf933285))
* resolve critical security and concurrency vulnerabilities ([d6c918f](https://github.com/Geogboe/boxy/commit/d6c918fb02d0c5b498f244a3d8499ecfc0caabc5))
* resolve SQLite schema issue in integration tests ([726ab26](https://github.com/Geogboe/boxy/commit/726ab266c9101a49207a4e50eae57a2dc59e595e))
* revert to golangci-lint v1 and only lint new issues ([93e0863](https://github.com/Geogboe/boxy/commit/93e0863328880777550dfaaaef37536c8200daff))
* **security:** use crypto/rand for password generation ([1c18c37](https://github.com/Geogboe/boxy/commit/1c18c373d15797252c4fe1abf1a71f4f635a12b6))
* set release-please baseline to v0.1.0 ([8de1ae3](https://github.com/Geogboe/boxy/commit/8de1ae3f69d47b770ec32465e82a99119edafb9c))
* update integration test helpers after GORM to database/sql migration ([0202232](https://github.com/Geogboe/boxy/commit/0202232ca45e7332b0f07ce2471f6363adb94da5))
* upgrade golangci-lint to v2, fix tampered ciphertext test ([8859836](https://github.com/Geogboe/boxy/commit/885983675089bf2997e1aa29b75f8d0da18f4978))
* use cross-platform date function in Taskfile ([d21a213](https://github.com/Geogboe/boxy/commit/d21a213617669e6e364dc23ea34cc6da4cd552c6))
* use local paths in boxy init instead of home directory ([799ad6f](https://github.com/Geogboe/boxy/commit/799ad6f041c700f9ee82e5f11ad5a00ec328a4bb))


### Refactoring

* **core:** consolidate provider registry on providersdk ([dcdfd58](https://github.com/Geogboe/boxy/commit/dcdfd58ea0ecc19570a6e6afd06d46c24381714b))
* **core:** group core into black-box packages ([2ec07c2](https://github.com/Geogboe/boxy/commit/2ec07c279e7118b49a4fc0074b8a27eb561f8b49))
* enhance Taskfile with improved build and test tasks ([2a29f13](https://github.com/Geogboe/boxy/commit/2a29f13af0518efa77653035cc1d72d5dee235e2))
* migrate from GORM to database/sql with modernc.org/sqlite ([d9a02be](https://github.com/Geogboe/boxy/commit/d9a02be80d8692d20f596e9eaa51a85a36d0bb60))
* **model:** inline ids/refs and add resource request ([5303f03](https://github.com/Geogboe/boxy/commit/5303f036eceef502fa9f803c81c26ad9dc19599a))
* **pool:** centralize provisioner seam ([59d6858](https://github.com/Geogboe/boxy/commit/59d68587eb96c0e44626d4eebca2b8dd64b9bdf7))
* **providers:** nest built-in drivers under providersdk ([eca5355](https://github.com/Geogboe/boxy/commit/eca5355db3dd0ab42f69933b6336a1d1821f45d8))
* redefine Driver as CRUD interface with Registration pattern ([c9dc55c](https://github.com/Geogboe/boxy/commit/c9dc55c92b380da28213f2cae4cad6a1441747c2))
* remove old driver implementations (docker, hyperv, process) ([6020396](https://github.com/Geogboe/boxy/commit/60203969e104ac8be3f8d46217ceeaae289c6083))
* rename devbox provider to devboxes ([5c9e056](https://github.com/Geogboe/boxy/commit/5c9e0569c1b0f168bce62492eab409f8162aee79))
* rename devboxes provider to devfactory ([475c670](https://github.com/Geogboe/boxy/commit/475c670d9c4601625d040525da403de657a34844))
* rename drivers/ to providers/ in providersdk ([ae4286b](https://github.com/Geogboe/boxy/commit/ae4286ba3eaa399160c65b6bb80fd60c13b8afe2))
* rename Execute() to Exec() and add E2E testing ([5c8622f](https://github.com/Geogboe/boxy/commit/5c8622f7cc147d9e41577c5aa376c236bf86f064))
* restructure internal packages for clearer architecture ([e739ad0](https://github.com/Geogboe/boxy/commit/e739ad0b6db93a4b80887d4b5b5208c370e11779))
* update quickstart examples and configuration for local usage ([bffb008](https://github.com/Geogboe/boxy/commit/bffb008973a1d220138a0d6072ad2bab11b44d7d))
* update resource type references to use pkg/provider ([47387d3](https://github.com/Geogboe/boxy/commit/47387d3145f70de63667c42b36a7dac625899712))
* update resource type references to use pkg/provider ([cdea2e4](https://github.com/Geogboe/boxy/commit/cdea2e499347adb49e422dd19843f26214df66d8))

## [0.1.9](https://github.com/Geogboe/boxy/compare/v0.1.8...v0.1.9) (2026-03-26)


### Features

* **install:** update install scripts for GoReleaser archive format ([e52631b](https://github.com/Geogboe/boxy/commit/e52631b12ad1f3d30cee6e4dd3af15ca67fc7326))


### Bug Fixes

* clean up cli lint issues ([f1627d1](https://github.com/Geogboe/boxy/commit/f1627d170b042e58a7f997bd68790cc4500dc27b))
* harden release installers ([ca8fac1](https://github.com/Geogboe/boxy/commit/ca8fac19a6295c063ffa59406e48fec1a551d641))
* **install:** correct ARM detection display and improve error handling ([58d76f1](https://github.com/Geogboe/boxy/commit/58d76f122db0e5cf13ff12d63cd828b3288e09b0))

## [0.1.8](https://github.com/Geogboe/boxy/compare/v0.1.7...v0.1.8) (2026-03-23)


### Bug Fixes

* harden installer release compatibility ([d1b7179](https://github.com/Geogboe/boxy/commit/d1b7179c6a6c974089d5538273b1ddb0ce17d747))

## [0.1.7](https://github.com/Geogboe/boxy/compare/v0.1.6...v0.1.7) (2026-03-21)


### Features

* add release installer scripts ([#48](https://github.com/Geogboe/boxy/issues/48)) ([35c257c](https://github.com/Geogboe/boxy/commit/35c257cb484eef55f8ed2db3651116ee32822324))

## [0.1.6](https://github.com/Geogboe/boxy/compare/v0.1.5...v0.1.6) (2026-03-20)


### Features

* allocation hooks, env file output, and spinner UX ([#45](https://github.com/Geogboe/boxy/issues/45)) ([76c3b36](https://github.com/Geogboe/boxy/commit/76c3b366a7471e48929b5454b31ddaf5fbec797e))
* human-friendly sandbox CLI output ([#43](https://github.com/Geogboe/boxy/issues/43)) ([573a10d](https://github.com/Geogboe/boxy/commit/573a10d1313d4f3555f6cde992e024049176285a))
* Hyper-V provider, pkg/vmsdk, pkg/psdirect, Docker SDK migration ([#46](https://github.com/Geogboe/boxy/issues/46)) ([63f7638](https://github.com/Geogboe/boxy/commit/63f763811dc859296989bb4580b95d3e123c7435))

## [0.1.5](https://github.com/Geogboe/boxy/compare/v0.1.4...v0.1.5) (2026-03-18)


### Features

* CLI UX overhaul — wireframe, init, status, serve banner ([#41](https://github.com/Geogboe/boxy/issues/41)) ([9df6b7e](https://github.com/Geogboe/boxy/commit/9df6b7e999b73d8df519e51d46ded793073fbe46))

## [0.1.4](https://github.com/Geogboe/boxy/compare/v0.1.3...v0.1.4) (2026-03-18)


### Features

* add agentsdk with Agent interface and EmbeddedAgent ([5e45797](https://github.com/Geogboe/boxy/commit/5e45797f72e9d2c6455f76506b715f34dbd9f8af))
* add devbox reference provider (in-memory CRUD driver) ([9b38fbe](https://github.com/Geogboe/boxy/commit/9b38fbe16d4c24112a9aa665ada9ae2792451a74))
* add docker pool and lab configuration examples ([1a96850](https://github.com/Geogboe/boxy/commit/1a96850ef270856735c192e3f25a1683fa8d022b))
* add JSON persistence, state machine, and CLI to devboxes provider ([4c62460](https://github.com/Geogboe/boxy/commit/4c62460b41fc2bb7635da64ae23d7316d66036f9))
* add pool and sandbox management features ([8ad66f5](https://github.com/Geogboe/boxy/commit/8ad66f5c638fd8157eaac577a3dc46277069f87b))
* add resource profiles to devboxes provider ([44f8a36](https://github.com/Geogboe/boxy/commit/44f8a3638673289cc2a5ce7e8686568caaae444a))
* **agent:** add reusable agent runner scaffold ([b96e419](https://github.com/Geogboe/boxy/commit/b96e419b4c547b4843ec53f710cbf69acd9db55c))
* **cli:** add minimal boxy serve ([cc7cd35](https://github.com/Geogboe/boxy/commit/cc7cd35d7f07f73360802870d929644c5baef4f8))
* enhance documentation and configuration for resource pooling ([50fe2c2](https://github.com/Geogboe/boxy/commit/50fe2c2cd2cbca0d391d741ddb4c3afaaca1fe16))
* **hooks:** add standalone hook runner package ([dad07c9](https://github.com/Geogboe/boxy/commit/dad07c9e14c0b694e42e856f047b6308941ec4dd))
* make sandbox create work with examples ([f64f34c](https://github.com/Geogboe/boxy/commit/f64f34cc554136663958913ca73919488d68c5e6))
* merge feat/cli — CLI restructure with config, sandbox, and debug provider commands ([e111a35](https://github.com/Geogboe/boxy/commit/e111a35136644aceab6c6904e8240422df33ef61))
* **model:** add resource profiles and pool constraints ([38bc438](https://github.com/Geogboe/boxy/commit/38bc438af1272725622822d0c50e3942a488ccd3))
* **pkg:** add resourcespec, resourcepool, and providersdk scaffolds ([8b6419a](https://github.com/Geogboe/boxy/commit/8b6419a5426ce4b062d8127e9825d8d6e2116415))
* **policycontroller:** add generic observe-decide-act loop ([c72a0c2](https://github.com/Geogboe/boxy/commit/c72a0c276d379dfc3598bd8d65ccb5b9928fda99))
* **providers:** add reusable docker and hyperv driver scaffolds ([495e5b1](https://github.com/Geogboe/boxy/commit/495e5b1e2ae8734ef49f29bf4b7b8eb2c97f936c))
* **providersdk:** add ExecSpec and process provider ([da1e5ac](https://github.com/Geogboe/boxy/commit/da1e5ac486c3b867abecb4d40a82cec090b0cd02))
* restructure CLI with config, sandbox, and debug provider commands ([80438d5](https://github.com/Geogboe/boxy/commit/80438d5ac7e452c0c0d7adfc3f234b40a3c9303a))
* update .gitignore and enhance README and ROADMAP documentation ([1e86e43](https://github.com/Geogboe/boxy/commit/1e86e43b6890af290b9c03ada01df870febf8959))
* update Docker pool examples and remove deprecated configurations ([b8861ec](https://github.com/Geogboe/boxy/commit/b8861ec8369592618820a5aa542c5b2d7c6b9aac))
* update pool configurations to use 'docker' type ([bfb7210](https://github.com/Geogboe/boxy/commit/bfb7210c2646c0a979efa33878e7c86f65de3438))


### Bug Fixes

* enforce one driver per provider type per agent ([f883d13](https://github.com/Geogboe/boxy/commit/f883d133457aead67ebc06ea34492e1685864d19))
