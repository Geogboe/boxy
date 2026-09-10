# Changelog

## [0.1.68](https://github.com/Geogboe/boxy/compare/v0.1.67...v0.1.68) (2026-09-10)


### Bug Fixes

* address Copilot review findings on PR [#367](https://github.com/Geogboe/boxy/issues/367) ([ebf1568](https://github.com/Geogboe/boxy/commit/ebf15685c00131a29ee6897438dde63641856826))
* **server:** close [#327](https://github.com/Geogboe/boxy/issues/327)'s visual-elegance gap against its mockups ([380c006](https://github.com/Geogboe/boxy/commit/380c006b53d001611adbc699ddab0b189d013ca8))


### Documentation

* note the windows-latest t.TempDir() cleanup flake found on PR [#367](https://github.com/Geogboe/boxy/issues/367) ([f982c45](https://github.com/Geogboe/boxy/commit/f982c4503120103c43f2676c96cf4b951a8b4df6))
* write up post-0.1.66 batch learnings (ADR, UI patterns, PR-review gate) ([a5f48d3](https://github.com/Geogboe/boxy/commit/a5f48d3fed5655d50bcca4dcd5d8c35814799014))

## [0.1.67](https://github.com/Geogboe/boxy/compare/v0.1.66...v0.1.67) (2026-09-09)


### Features

* **config:** add per-operation agent timeouts and provisioning watchdog threshold ([#333](https://github.com/Geogboe/boxy/issues/333), [#337](https://github.com/Geogboe/boxy/issues/337)) ([726f847](https://github.com/Geogboe/boxy/commit/726f847d97620491c18b088a2e7d375f84be567c))
* **pool:** add stuck-provisioning watchdog to reconcile loop ([#337](https://github.com/Geogboe/boxy/issues/337)) ([4a20f4c](https://github.com/Geogboe/boxy/commit/4a20f4cdafb1e04f4d791e863c818ad9584fc73a))
* **pool:** bound agent Create/PersonalizeGuest/Delete and quarantine on personalize timeout ([#333](https://github.com/Geogboe/boxy/issues/333)) ([925bcd5](https://github.com/Geogboe/boxy/commit/925bcd5bee05c244fa2b35cedeeaf53fdc359ab1))
* **sandbox:** isolate per-sandbox fulfillment and quarantine timed-out allocations ([#333](https://github.com/Geogboe/boxy/issues/333)) ([e318d94](https://github.com/Geogboe/boxy/commit/e318d943dad9fa8f4a727065c2bcaf05f2936583))
* **ui:** remodel /ui/pools to a collapsed-by-default summary view ([18bc886](https://github.com/Geogboe/boxy/commit/18bc886639857b09fdaf8b511491f1b92167e532))
* **web-ui:** rename agent-facing verbiage to host ([#332](https://github.com/Geogboe/boxy/issues/332)) ([c44fba5](https://github.com/Geogboe/boxy/commit/c44fba59bfb3066b40d94cc3f498f3c9f2c9b439))


### Bug Fixes

* **auth:** unblock user-role CLI login and pool discovery ([#359](https://github.com/Geogboe/boxy/issues/359)) ([3c8f92e](https://github.com/Geogboe/boxy/commit/3c8f92ee14a38617e8388cf3204c69d4edd6452f))
* **config:** reserve "unassigned" pool name; add rel=noopener to repo link ([becbcae](https://github.com/Geogboe/boxy/commit/becbcae8991754ca138fdadcd209780a76c5c802))
* **hyperv:** defer network IP application to allocation time ([#358](https://github.com/Geogboe/boxy/issues/358)) ([6eb4004](https://github.com/Geogboe/boxy/commit/6eb40040ff4fd9253d593b0649ee1d4e0b5d9a4d))
* **hyperv:** restore admission-time fail-fast for Linux + boxy-managed IP mode ([c03605c](https://github.com/Geogboe/boxy/commit/c03605c4cc3bdca43c63cafbdbfc1226c952481f))
* **hyperv:** reuse one PSRP session across apply_network+rotate_credential ([#361](https://github.com/Geogboe/boxy/issues/361)) ([b986f6b](https://github.com/Geogboe/boxy/commit/b986f6b870df8cfe01b235eccb1f8edd3017ce96))
* **pool:** surface quarantine-exhausted pools instead of going silent ([#328](https://github.com/Geogboe/boxy/issues/328)) ([cc6d5b5](https://github.com/Geogboe/boxy/commit/cc6d5b5641683760a4c1068f06f0b7bc556dbc6f))
* **server:** close [#327](https://github.com/Geogboe/boxy/issues/327) UI-validation findings (expand state, badge, footer) ([18019bc](https://github.com/Geogboe/boxy/commit/18019bc3daae1718734f3b486cdd36565598317e))
* **server:** set CSRFToken in fragmentHandler for polled forms ([b3f4db8](https://github.com/Geogboe/boxy/commit/b3f4db8be1eb8d676b510e654ed16fd713a47e6f))
* **web-ui:** hide destroyed/released resources from default views ([#353](https://github.com/Geogboe/boxy/issues/353)) ([9a4b341](https://github.com/Geogboe/boxy/commit/9a4b3419d762c030efb8b63b0d9c5e9858a5abad))


### Testing

* **hyperv:** use scanner-safe placeholder for [#361](https://github.com/Geogboe/boxy/issues/361)'s session-reuse test fixtures ([b1ea6ad](https://github.com/Geogboe/boxy/commit/b1ea6ad410f9f322731dbd84f3ff306c281bd050))

## [0.1.66](https://github.com/Geogboe/boxy/compare/v0.1.65...v0.1.66) (2026-09-08)


### Features

* **web-ui:** add an all-resources nav page ([1519797](https://github.com/Geogboe/boxy/commit/1519797e9dd67fcb5afb97bb1f5b21dd5c22e659))


### Bug Fixes

* address Copilot review findings on PR [#340](https://github.com/Geogboe/boxy/issues/340) ([7448ab8](https://github.com/Geogboe/boxy/commit/7448ab81b9c555c0e789071d509362d79199e8f7))
* **agent:** install the diagnostics-wrapping logger as slog default ([20a86e0](https://github.com/Geogboe/boxy/commit/20a86e054b78e88310d470f8efc2d5815a515cd5)), closes [#334](https://github.com/Geogboe/boxy/issues/334)
* **cli:** install a concrete slog default for the whole agent test file ([dedf394](https://github.com/Geogboe/boxy/commit/dedf3940dd2beedd045e667db11f674991e7b706))
* **diagnostics:** use relative timestamps in retention pagination test ([6bf5d28](https://github.com/Geogboe/boxy/commit/6bf5d28bbe920a2adde9e8f2251de9967cd4c2ea)), closes [#341](https://github.com/Geogboe/boxy/issues/341)
* **serve:** advertise only pool-required providers from the embedded agent ([0668f2b](https://github.com/Geogboe/boxy/commit/0668f2b6ef85933ae41500735e9f40aa94b3ab7e))
* **serve:** recognize an explicit "embedded" agent pin in embeddedProviderTypes ([beaa72b](https://github.com/Geogboe/boxy/commit/beaa72b1a6b2e9f10b2506d7174be7f9952b4a71))
* **web-ui:** cap the all-resources page and hide zero CreatedAt ([eabcc2b](https://github.com/Geogboe/boxy/commit/eabcc2b9fe399f6a00e522765663ae557e5a2ac1))


### Documentation

* generalize local Hyper-V test host name in checked-in docs ([53929fd](https://github.com/Geogboe/boxy/commit/53929fd282af0ca11a71e9656d0af2f38bdbc6ea))

## [0.1.65](https://github.com/Geogboe/boxy/compare/v0.1.64...v0.1.65) (2026-09-06)


### Features

* **diagnostics:** add spec-required Provider field end-to-end ([a27ba11](https://github.com/Geogboe/boxy/commit/a27ba116cc64352802b00cdda827e7fdde2a2a8c))
* **diagnostics:** add structured cached timelines ([28c4c59](https://github.com/Geogboe/boxy/commit/28c4c59c8040df260a5607ff2761e05db2b631fe))
* **diagnostics:** bridge pool and agent job steps into diagnostics ([655c614](https://github.com/Geogboe/boxy/commit/655c614c162bf15102676067d3351925d8f93fba))
* **diagnostics:** track remote agent log pulls ([97ad6fe](https://github.com/Geogboe/boxy/commit/97ad6fe1f59c677829f16d27a0ec9779161298f8))
* **hyperv:** emit structured diagnostics for personalization/memory failures ([4ad2796](https://github.com/Geogboe/boxy/commit/4ad2796844195f5878ef18a9a74e2533f69d619e))
* **hyperv:** enforce provider memory budget ([1beec7f](https://github.com/Geogboe/boxy/commit/1beec7f7614ddad715b61004893d6ce24ea3b8bc))
* **jobs:** add durable public job runner ([d7b0dc2](https://github.com/Geogboe/boxy/commit/d7b0dc226d10c3f8348996b5120edd4b4dbb5ea1))
* **pool:** add opt-in retain-for-debug retry, correct spec text ([37ea8c0](https://github.com/Geogboe/boxy/commit/37ea8c09c492e3a7c62f2ab7aca40dbbf729dfb3))
* **pool:** add Save-and-Apply configuration API and UI ([e5e4cf8](https://github.com/Geogboe/boxy/commit/e5e4cf8423856062c64f1500b2b8b61113166259))
* **pool:** run operations as durable jobs ([9c3e46b](https://github.com/Geogboe/boxy/commit/9c3e46bfd2aa2e615776a6ffbebdb60e25191d3c))
* **store:** persist generic jobs ([0458288](https://github.com/Geogboe/boxy/commit/04582884e48ccd24db9318fb73d73132b4412ee2))


### Bug Fixes

* address Copilot review findings on PR [#338](https://github.com/Geogboe/boxy/issues/338) ([b812734](https://github.com/Geogboe/boxy/commit/b8127346c1e5d4f80a01541d211ee85b19f0aa36))
* **diagnostics:** don't show routine step text as an error ([971fb06](https://github.com/Geogboe/boxy/commit/971fb06f2bcf2d44a1d31626ba18d74f4826c4fd))
* **hyperv:** serialize PersonalizeGuest per resource ([60f7840](https://github.com/Geogboe/boxy/commit/60f784061193e9fe799c66b488f9e8a8ccbcd811))
* **hyperv:** use scanner-safe placeholders for new test fixtures ([413ac57](https://github.com/Geogboe/boxy/commit/413ac572b5f1092681b58d9c3ac5914b2d48f6ba))
* **lint:** resolve golangci-lint findings surfaced by this batch's own code ([6a47b99](https://github.com/Geogboe/boxy/commit/6a47b99a339d56554b4b0c4bca7d26b2067c620a))
* **pool:** report exhausted failed capacity ([d61ae73](https://github.com/Geogboe/boxy/commit/d61ae739741e34cedf2448bb883785edb99aaf84))
* **pool:** retry packages without rotating credentials ([e7c5b78](https://github.com/Geogboe/boxy/commit/e7c5b78cdeb47b3dda3b822fd507ebe90e4e4e72))
* **serve:** don't fail startup over an unconfigured provider type ([d931188](https://github.com/Geogboe/boxy/commit/d931188485a4c24c3bea1a6dfd5a12797b27ce2b))
* **server:** stop racing the shared execution struct in sandbox exec jobs ([fbf1904](https://github.com/Geogboe/boxy/commit/fbf19040b7a9c4bcb9cc42ed42d10c30700b0000))


### Refactoring

* **sandbox:** share durable job lifecycle ([08a8af6](https://github.com/Geogboe/boxy/commit/08a8af65951691fa2e709cea8253aa3f4220cc99))


### Documentation

* track next-release progress notes for session continuity ([57b832b](https://github.com/Geogboe/boxy/commit/57b832bfb2fef19a4402a2bda3f802331a81edec))
* update progress notes after diagnostics bridge and [#336](https://github.com/Geogboe/boxy/issues/336) fix ([9eaceeb](https://github.com/Geogboe/boxy/commit/9eaceebff6a3e6580011362260d8fccb3de64170))

## [0.1.64](https://github.com/Geogboe/boxy/compare/v0.1.63...v0.1.64) (2026-09-04)


### Features

* **cli:** add admin pool direction aliases ([38ccbb1](https://github.com/Geogboe/boxy/commit/38ccbb1a7b11bb3c39ab49118bcd56a1ca65df62))
* **cli:** add admin pool listing ([4804e8c](https://github.com/Geogboe/boxy/commit/4804e8c5f93ce6ddd02968a9555aad2d0adcea9e))
* **cli:** add admin pool maintenance commands ([28594a3](https://github.com/Geogboe/boxy/commit/28594a33678dbb2fe34104d3aa83c0f85803ae20))
* **cli:** expose resource maintenance to admins ([a65f1cf](https://github.com/Geogboe/boxy/commit/a65f1cfa4f6e3f627bdf75a138fc4c5c2e0f77d0))
* **cli:** support plural admin pool commands ([cdc24d9](https://github.com/Geogboe/boxy/commit/cdc24d95d40a9ba19f37053334e847bd3cf28b4c))
* **pool:** route by reported agent headroom ([2b2290c](https://github.com/Geogboe/boxy/commit/2b2290c7697f6dbcd26209cab84edc2f075003e0))
* pull remote agent diagnostics on demand ([bf58f7d](https://github.com/Geogboe/boxy/commit/bf58f7d461aebd87994d2d10806ccfc6516b059f))
* **ui:** paginate diagnostics history ([cd7c7a0](https://github.com/Geogboe/boxy/commit/cd7c7a0ea9875f47b864b07937867c1539bea4b0))


### Bug Fixes

* **pool:** simplify fallback agent selection ([7ac78c0](https://github.com/Geogboe/boxy/commit/7ac78c0bc1fd88d5dace994a64ac753524ffb9a5))
* **release:** align workflow with merged dependency update ([c78ad25](https://github.com/Geogboe/boxy/commit/c78ad2553805446aa6da52d515935e9f299bcf6e))
* **release:** generate complete release notes ([6a10827](https://github.com/Geogboe/boxy/commit/6a10827de7678794ce4dd1b6a33cbba451943edc))
* **resourcepack:** skip applied packages before event validation ([19a23ad](https://github.com/Geogboe/boxy/commit/19a23ad8b7803f83586d712a96efa1ca35abcf92))
* **ui:** add repository footer ([ddac118](https://github.com/Geogboe/boxy/commit/ddac118ea6f92cf53eb54923cac871bb56815b07))
* **ui:** collapse pool resource groups by default ([082bcee](https://github.com/Geogboe/boxy/commit/082bcee0ae1ecf5ed81cc424ef926370cdf8fc83))
* **ui:** make agent log views actionable ([ff07982](https://github.com/Geogboe/boxy/commit/ff07982a1f5aa9a4a286e76b27af19c82e46921a))
* validate package requests before allocation ([74f10a0](https://github.com/Geogboe/boxy/commit/74f10a0addbcb79130470331c24bdba54c9bc1f1))


### Refactoring

* extract atomic fulfillment transaction ([3435ae3](https://github.com/Geogboe/boxy/commit/3435ae32b61780bafd1a0f29b6f9c7e865ec9813))


### User Interface

* **ui:** format dashboard view models ([cbe05f8](https://github.com/Geogboe/boxy/commit/cbe05f866d5a98a092c9fc449243085fa69aa630))


### Documentation

* **cli:** document admin pool listing ([d195a15](https://github.com/Geogboe/boxy/commit/d195a15656f77f8dae58631d01088b0b77e79923))
* document agent log collection command ([944e0f4](https://github.com/Geogboe/boxy/commit/944e0f4df8c141dffd29985fdba1844294ae972d))
* specify on-demand agent log pulls ([38271c1](https://github.com/Geogboe/boxy/commit/38271c1e96d35cebc55dc64c3baa5958061f55c7))
* **ui:** define dashboard design language ([4e03c18](https://github.com/Geogboe/boxy/commit/4e03c180de0d2eaf3e50f4aaf529d3ad3b99c2ab))
