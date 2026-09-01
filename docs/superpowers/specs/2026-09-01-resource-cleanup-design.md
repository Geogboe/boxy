# Resource cleanup and purge (#292)

## Shared service

`pool.ResourceCleanupService` is the single selection and mutation boundary.
The CLI calls its administrator-only REST endpoint, while the dashboard uses
the same endpoint for its preview and force workflow. A cleanup request is
dry-run by default and carries an actor, `force` flag, and optional `dry_run`
flag.

Destroyed resources are candidates only when no sandbox references their ID.
Forced cleanup additionally considers `destroying` and `error` resources whose
last update is at least 30 minutes old. Ready, allocated, provisioning,
promoting, and recent transient/error records are skipped. Any sandbox
reference blocks cleanup regardless of mode.

Dry-run reports candidate, skipped, and error records without changing state.
Mutation purges destroyed records after removing them from every pool
inventory. Forced records reuse `Manager.DestroyResource`, which persists the
destroying transition and routes idempotent provider deletion through the
resource's owning agent. A failed resource remains recorded and appears in
the report.

Every mutation emits one redacted audit event containing actor, mode, force
state, selection criteria, and counts. It never includes provider credentials,
script contents, or arbitrary resource properties.

## HTTP and CLI

`POST /api/v1/resources/purge` is administrator-only and accepts
`{"dry_run":true,"force":false}`. The response contains `dry_run`,
`force`, candidate count/IDs, cleaned IDs, skipped records, and individual
errors. The CLI exposes `boxy debug resource purge --dry-run` and
`boxy debug resource purge --force`; omission of `--force` never touches
destroying/error records.
