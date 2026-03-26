# Changelog

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
