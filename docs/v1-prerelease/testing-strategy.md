# Testing Strategy - v1 Prerelease Notes

> ARCHIVED: This document was moved to `docs/planning/v1-prerelease/testing-strategy.md`; canonical testing strategy is `docs/testing-strategy.md`.

## History

```yaml
Origin: "docs/v1-prerelease/testing-strategy.md"
SourceType: "migration-notes"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Migrated testing specifics to planning and canonical testing strategy consolidated in `docs/testing-strategy.md`."
```

This document contains specific testing considerations and examples relevant to the v1 prerelease development. For the comprehensive and general testing strategy of Boxy, please refer to the main document:

**[Comprehensive Testing Strategy for Boxy](../testing-strategy.md)**

---

## v1 Specific Considerations

- **TDD Focus**: v1 Prerelease uses Test-Driven Development with an emphasis on integration and E2E tests.
- **Hyper-V Stubbing**: A key part of the v1 strategy is using a `StubHyperVProvider` to allow for testing of the distributed architecture on Linux CI environments.
- **Regression Testing**: All v1-prerelease functionality must be covered by regression tests to ensure the architectural refactor does not break existing features.

### Key v1 Test Scenarios

- **Integration**: `TestIntegration_FullAllocationFlow`, `TestPreheating_DockerContainers`, `TestRecycling_RollingStrategy`, `TestMultiTenancy_QuotaEnforcement`, `TestAgent_DockerViaRemote`.
- **E2E**: `TestE2E_QuickTestingUseCase`, `TestE2E_CIRunnerUseCase`, `TestE2E_DistributedAgent_StubHyperV`.
- **Manual**: A full manual test pass on a Windows host with a real Hyper-V provider is required before v1 release.
