# ADR-013: Configuration Schema Standardization

**Date**: 2025-11-23
**Status**: Proposed

## Context

The v1-prerelease proposed a more formal configuration schema for Boxy, to validate and document all top-level configuration options, pools, providers, and hooks. A schema enables validation (before runtime), better UX for editors, and consistent migration across versions.

## Decision

1. Standardize configuration schema as an OpenAPI-like schema or JSON schema file `docs/config-schema.json`.
2. Validate configuration at startup (or early) and provide detailed validation errors to the user.
3. Add validation tests for the configuration schema and reference the schema in `docs/guides/getting-started.md`.

## Implementation

- Draft `docs/config-schema.json` with all keys: `server`, `agents`, `pools`, `hooks` with examples and types.
- Add a `boxy config validate` command that checks a config file against the schema and returns errors.
- Incorporate schema validation into `boxy serve` startup path in order to fail-fast on malformed config.

## Consequences

- Users get immediate feedback for config issues.
- Editor integrations and auto-complete become feasible with editor schemas.
- This adds maintenance overhead for schema updates across versions.

## History

```yaml
Origin: "docs/v1-prerelease/08-config-schema.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Create a canonical schema and enforce validation in startup and a `config validate` command."
```
