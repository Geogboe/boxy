# Planning: Developer Roadmaps & Proposals

This directory contains planning artifacts, proposals, and in-progress design documents for the Boxy project. These files are *not* canonical — they capture ideas, migration steps, and developer notes that may later be converted into ADRs or final design docs.

Purpose:

- Collect planning documents (e.g., `v1-prerelease` items)
- Provide a single index and guidance for where to record future plans
- Make it easy to find which planning items are active, completed, or archived

Use this workflow:

1. Draft proposals in `docs/planning/` (status: `draft`) or a sub-folder like `docs/planning/v1-prerelease/`.
2. When a decision is finalized, create an ADR in `docs/decisions/` and update the roadmap.
3. Move implemented or stale planning items to `docs/archive/` or mark them `ARCHIVED` in place.

This directory is for internal dev use; it is intentionally writable and informal compared to `docs/architecture/` and `docs/decisions/`.

See also:

- `docs/decisions/` — Finalized Architecture Decision Records (ADRs)
- `docs/roadmap/` — High-level roadmap and release plans (if present)
- `docs/planning/v1-prerelease/` — v1 planning artifacts

Document Provenance & History

We track the provenance of planning documents so readers can quickly understand where content originated and whether it has been migrated into canonical documents (ADRs, architecture, or guides).

Recommended frontmatter / header `History` fields for planning docs:

- `Origin`: Original location or context (e.g., `docs/v1-prerelease/migration-guide.md`)
- `SourceType`: `proposal` | `migration-notes` | `planning-issue` | `example`
- `Created`: `YYYY-MM-DD` (approximate if exact unknown)
- `CreatedBy`: `GitHub username`, `author` or `unknown`
- `LastMigrated`: `YYYY-MM-DD` (date moved into `docs/planning/` or other)
- `Status`: `draft` | `proposed` | `implemented` | `archived`
- `Notes`: Short human-friendly reason or link to PR/issue for migration

Example `History` header (to include at top of file):

````yaml
History:
 Origin: "docs/v1-prerelease/02-preheating-recycling.md"
 SourceType: "migration-notes"
 Created: "2024-11-22"
 CreatedBy: "unknown"
 LastMigrated: "2025-11-23"
 Status: "proposed"
 Notes: "Moved from v1-prerelease into planning; proposed ADR-009 created for canonical semantics"
````
