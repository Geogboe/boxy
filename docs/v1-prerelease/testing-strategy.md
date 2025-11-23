# Testing Strategy - v1 Prerelease Notes

This document contains specific testing considerations and examples relevant to the v1 prerelease development. For the comprehensive and general testing strategy of Boxy, please refer to the main document:

[Comprehensive Testing Strategy](../testing-strategy.md)

## v1 Specific Considerations

### Overview

v1 Prerelease uses **Test-Driven Development** with emphasis on:

- **Smoke tests** - Quick sanity checks
- **Integration tests** - Component interactions with real providers
- **E2E tests** - Full user workflows
- **Stubs/mocks** - Test without unavailable harness (Hyper-V on Linux)

**Philosophy**: Test incrementally as features are built, not all at end.

### Stub/Mock Strategy for Hyper-V

**Problem**: Hyper-V only runs on Windows, but CI often runs on Linux.
**Solution**: Utilize a `StubHyperVProvider` that simulates Hyper-V behavior on Linux, allowing for broader integration and E2E testing of Hyper-V related features without a Windows environment.

### Integration Examples Relevant to v1

Specific integration test examples for v1 features like allocator, preheating, recycling, multitenancy, and distributed agent:

-   `TestIntegration_FullAllocationFlow`
-   `TestPreheating_DockerContainers`
-   `TestRecycling_RollingStrategy`
-   `TestMultiTenancy_QuotaEnforcement`
-   `TestAgent_DockerViaRemote`

### E2E Examples Relevant to v1

Specific end-to-end test examples for v1 use cases:

-   `TestE2E_QuickTestingUseCase` (Simulates primary use case)
-   `TestE2E_CIRunnerUseCase` (Simulates CI/CD runner use case)
-   `TestE2E_DistributedAgent_StubHyperV` (Tests distributed setup with Hyper-V stub)
-   `TestE2E_SandboxWithNewArchitecture` (Regression test for architectural refactor)

### Manual Testing for Hyper-V

**Required**: At least once before v1 release.
**Setup**: Requires a Windows Server with Hyper-V enabled, Boxy agent installed on Windows, and a Linux server with Boxy server configured with mTLS certificates.
**Test Cases**: Manual verification of agent connection, Hyper-V pool provisioning, preheating, sandbox creation, RDP connection info, and VM cleanup.

---

**Last Updated**: 2025-11-23
**Review**: Continuous throughout v1 implementation