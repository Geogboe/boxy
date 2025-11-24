# ADR-012: CLI UX & API Schema

**Date**: 2025-11-23
**Status**: Proposed

## Context

The v1-prerelease planning contains proposals for CLI UX changes and the introduction of a REST API with a machine-readable OpenAPI schema. This decision captures the approach to finalize CLI commands and API schemas and ensures stability for client integrations.

## Decision

1. **CLI Command Stability**: The CLI command tree (`boxy serve`, `boxy pool`, `boxy sandbox`, `boxy admin`) will be finalized and considered stable for v1 with additive changes only (no breaking changes unless behind feature flags and with migration guides).
2. **OpenAPI/REST API**: Implement a versioned HTTP REST API covered by OpenAPI (v1 as a minimum). The API will be read-only for v1 with write operations in v2 (planned), and an initial OpenAPI spec will be generated as part of `docs/openapi.yaml`.
3. **Client Libraries**: Encourage client auto-generation from OpenAPI; client SDKs should point to `docs/openapi.yaml`. The spec must be generated at build time and included in docs site.
4. **Documentation & Tests**: CLI documentation will be updated in `docs/CLI_UX.md` and the OpenAPI spec will be validated with integration tests.

## Implementation Plan

- Finalize CLI commands and confirm UX with maintainers.
- Add `docs/openapi.yaml` generation step to the docs/build pipeline.
- Add CI checks to validate the OpenAPI spec against server handlers.

## Consequences

- Encourages users and tooling to depend on stable CLI and API contracts.
- Requires generation infrastructure for OpenAPI and integration tests.

## History

```yaml
Origin: "docs/v1-prerelease/09-cli-api-schemas.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Finalize CLI commands and create an OpenAPI spec for API stability."
```
