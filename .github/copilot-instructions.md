# Boxy AI Coding Assistant Instructions

> Goal: Enable immediate productive work on Boxy without re-reading the entire docs set. Focus on current MVP + emerging distributed/agent architecture.

## Big Picture

Boxy is a Go 1.24+ service + CLI that maintains warm resource pools (VMs, containers) and allocates ephemeral, non-reused resources into time‑bound sandboxes. Future: distributed agents exposing remote providers over gRPC with mTLS.

## Core Architecture Surfaces

- `cmd/boxy/` Cobra CLI entry; version injected via ldflags.
- `internal/core/` domain logic: pool, sandbox, resource state machines & background workers (replenish, health, cleanup).
- `internal/provider/` concrete implementations (e.g., Docker); **keep providers dumb** – pure CRUD + Execute.
- `pkg/provider/` shared provider interfaces + (future) remote proxy + protobuf definitions.
- `internal/storage/` persistence (SQLite via GORM) – resource, pool, sandbox metadata; avoid leaking provider internals into storage schemas.
- `internal/config/` YAML + Viper loading; precedence: flags > env > file.
- `docs/architecture/*` design authorities (MVP, distributed agents, hooks).

## Resource Lifecycle (Never Reuse)

Provisioning → Ready (in pool) → Allocated (sandbox) → Destroyed (always removed, not returned). Any failure during hooks or timeouts must destroy the resource to preserve cleanliness.

## Hooks Model

Two phases only in MVP:
- `after_provision` (finalization, slow, background) – heavy setup, validation.
- `before_allocate` (personalization, fast, user waiting) – credentials, hostname, lightweight tweaks.
Implement via `Provider.Execute`; keep personalization < ~10s; move slow work to base image or finalization.

## Distributed Agent Pattern (Emerging)

Remote providers proxied via gRPC (RemoteProvider) to agents exposing local backend (e.g., Hyper-V on Windows). Single binary runs server or agent. Security: mTLS with CA + short‑lived certs; plan token→cert issuance. When adding remote logic, preserve backwards compatibility (local providers unchanged).

## Concurrency & Safety

Pool replenishment and hook execution are background; ensure race safety on resource state transitions. Prefer channel or mutex patterns native to Go; avoid ad‑hoc sleeps. Timeouts defined per phase + per hook; exceeding triggers destroy.

## Adding a Provider

1. Implement `Provider` interface (Provision, Destroy, GetStatus, GetConnectionInfo, Execute, HealthCheck, Name, Type). Keep side effects minimal.
2. Map provider IDs (container ID, VM name) in `Resource` metadata; never store credentials in plaintext – encrypt or omit.
3. Support Execute for hooks; capture stdout/stderr; return non‑zero exit codes distinctly.
4. Register provider with factory wiring (local first; remote later via proxy wrapper).

## Build & Test Workflow

Use Taskfile tasks:
- Build: `task build` (ldflags sets Version/GitCommit/BuildDate).
- Unit tests fast: `task test:unit`.
- Integration (Docker required): `task test:integration`.
- E2E (must pass before feature completion claims): `task test:e2e`.
- Race + lint + fmt composite: `task check`.
Run selective tests before committing; do not skip E2E for lifecycle changes.

## CLI/API Alignment

Every CLI command should map cleanly to planned REST endpoints (see MVP_DESIGN). When extending CLI, describe expected API parity in PR description.

## Configuration Conventions

- Pools specify `min_ready`, `max_total`; replenishment triggers when ready < min.
- Credentials modes: auto/fixed/ssh_key (future). Default: auto with random password.
- Avoid embedding heavy logic in code; prefer declarative YAML + hooks.

## Performance Targets (Guide Design)

Personalization <10s (hard upper bound 30s); Provisioning timeouts configurable; slow provider operations must surface progress via logs (debug level) – do not silently block.

## Logging & Debugging

Use structured logging (logrus). Log state transitions, hook start/end, durations, but never plaintext secrets. Provide debug helpers (e.g., pool/sandbox test commands) rather than one‑off debug prints.

## Commit & PR Guidelines

Conventional commits (`feat:`, `fix:`, `refactor:` etc.). Small focused changes. Architecture-impacting changes reference relevant docs (e.g., ADR-004) and update diagrams if flows change.

## Common Pitfalls to Avoid

- Reusing a resource after sandbox destroy (violates cleanliness invariants).
- Slow operations inside `before_allocate` causing poor UX.
- Provider performing orchestration logic (belongs in core managers).
- Storing credentials or hook outputs unencrypted / in logs.
- Introducing blocking waits without timeouts.

## Fast Start Checklist for Agents

1. Read this file + `README.md`.
2. Inspect `internal/core/pool` for replenishment patterns before modifying.
3. Run `task test:e2e` to establish baseline.
4. Implement change; rerun unit + race + E2E; verify lifecycle invariants.

Feedback welcome: highlight unclear sections or missing high‑leverage patterns.
