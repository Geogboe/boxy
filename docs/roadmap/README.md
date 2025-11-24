# Boxy Roadmap

This document summarizes the current roadmap for Boxy and links to planning documents and ADRs for each major item. This is intended for internal developer planning and visibility.

Status keys:

- `planned` - Feature is planned but not started
- `in-progress` - Work is underway
- `completed` - Implementation merged and documented
- `blocked` - Waiting on dependencies or decisions

## v1-prerelease Roadmap

| Feature | Status | ADR / Docs | Notes |
|---------|:------:|-----------:|-------|
| Pool / Sandbox Peer Architecture (Allocator) | completed | [ADR-005](../decisions/adr-005-pool-sandbox-peer-architecture.md) | Implemented; core allocator introduced |
| Preheating & Recycling | proposed | [ADR-009](../decisions/adr-009-preheating-and-recycling.md) | Preheating implemented in pool manager; ADR-009 finalizes semantics |
| Hook Semantics (`on_provision`, `on_allocate`) | proposed | [ADR-008](../decisions/adr-008-terminology-on_provision-on_allocate.md) | Standardized naming; migrate configs |
| Distributed Agents (gRPC + mTLS) | in-progress | [ADR-004](../decisions/adr-004-distributed-agent-architecture.md) | Agent server implemented; remote provider available |
| Multi-Tenancy | proposed | [ADR-010](../decisions/adr-010-multi-tenancy.md) | Requires additional schema & auth middleware |
| CLI & API Changes | planned | `docs/v1-prerelease/*` | CLI UX changes proposed; REST API planned for v2 |
| Config Schema / Location | planned | `docs/v1-prerelease/08-config-schema.md` | Proposal to standardize config path; reconcile with code and ADR if finalized |
| Config Location & Discovery | proposed | [ADR-011](../decisions/adr-011-config-location.md) | Formalize search order and migration path; deprecate old user path with warning |
| CLI & API Schema | proposed | [ADR-012](../decisions/adr-012-cli-api-schema.md) | Stable CLI & OpenAPI spec; generate `docs/openapi.yaml` |
| Testing Strategy & Testrunner | completed | [docs/testing-strategy.md](../testing-strategy.md) | Consolidated testing strategy in `docs/testing-strategy.md` |

## How to use this roadmap

- If a feature is `proposed`, check the linked ADR and `docs/planning/v1-prerelease` for the proposed design and migration notes.
- If a feature is `in-progress`, look for PRs and issues linking the ADR to implementation.
- When a feature is `completed`, move the planning docs to `docs/archive/` and link the ADR in the `Roadmap` table.

## Owners & Contacts

- Primarily: project maintainers (see `CONTRIBUTING.md` for code ownership details)
- For each row, maintainers or owners should link PRs/issues in the relevant ADR and update the status in this file.
