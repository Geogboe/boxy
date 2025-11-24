# ADR-010: Multi-Tenancy

## History

```yaml
Origin: "docs/v1-prerelease/04-multi-tenancy.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
CreatedBy: "doc-reconciliation"
Status: "proposed"
Notes: "Derived from v1-prerelease multi-tenancy planning; captures the intended multi-tenant model."
```

**Date**: 2025-11-23
**Status**: Proposed

## Context

Multi-tenancy is a requirement for production usage of Boxy in environments shared by multiple teams or users. The v1-prerelease contained detailed migration notes and a proposed schema, but those proposals were not yet captured as an ADR and need an official design record.

## Decision

We will implement a role-based, tenant-aware system with the following attributes:

- **User & Team Model**: Users belong to teams, sandboxes are owned by a user and optionally a team.
- **Role-Based Access**: `admin` / `user` roles, where `admin` can manage pools, users, and teams; `user` can allocate sandboxes and view resources they own.
- **API Token Authentication**: Simple bearer API tokens for automation; tokens stored hashed in DB and expire upon rotation or revocation.
- **Quotas**: Configurable per-team or per-user quotas for concurrent sandboxes and resource counts.
- **Migration**: `boxy migrate add-user-table` will create user and team tables and backfill owners for existing sandboxes using a `system` owner if necessary.

## Security

- API tokens are generated with secure randomness (`crypto/rand`), stored hashed, and rotated on demand.
- All operations are audited with owner modification logs.

## Consequences

- Adds DB schema changes (users, teams, ownership columns on sandboxes).
- Adds an authentication and authorization layer to the API (server & CLI).
- Backward compatibility: default mode remains single-tenant if the admin hasn't enabled multi-tenancy.

## Implementation Tasks

1. Add User/Team DB schema and migration.
2. Add CLI `boxy admin` commands for user/team management.
3. Ensure the server has authentication middleware and that CLI forwards tokens.
4. Add tests for multi-tenant isolation and quotas.

## Related Docs

- `docs/planning/v1-prerelease/04-multi-tenancy.md`
- `docs/decisions/adr-003-configuration-state-storage.md` (storage considerations)
