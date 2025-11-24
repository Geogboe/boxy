# Provider Bootstrap (internal/bootstrap/providers)

Purpose: build and return a populated provider registry for the CLI entrypoints (serve, sandbox) with minimal logic in the command layer.

Responsibilities:

- Inspect pool configs to determine which backends are needed (scratch/shell, hyperv, etc.).
- Create provider instances with any per-pool extra_config (e.g., scratch base_dir/allowed_shells).
- Run lightweight availability checks (e.g., Docker health) and skip registration if unavailable.
- Return the registry to callers; no side effects beyond provider construction and health checks.

Non-goals:

- CLI flag parsing
- Pool manager orchestration
- Persistent state/storage concerns
