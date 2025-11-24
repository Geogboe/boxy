# ADR-006: Runner Provider (Process Containment)

**Date**: 2025-11-23  
**Status**: Superseded by ADR-007 (Scratch Provider Architecture)

## Context

- Docker and Hyper-V providers are hard to exercise in local dev and CI, making lifecycle and hook testing painful.  
- We need a provider that behaves like a real backend (provision → allocate → destroy, hooks, preheating) but runs entirely on the host with no external virtualization dependency.  
- Naming must avoid confusion with “local Docker/Hyper-V”; we also want first-class Linux and Windows support from day one.

## Decision

- Introduce a new compiled-in provider type named **`runner`**.  
- Require an `os` field per pool: `linux`, `windows`, or explicit `auto`. Agents validate at startup; pools whose `os` does not match the host are marked unsupported (logged and skipped). `auto` opts into “follow host” for the rare case it is wanted.  
- Execution model: process containment, not a strong security boundary. Defaults: network off where enforceable, empty env with allowlist, hard timeouts, stdout/stderr size caps, per-resource temp directories, and cleanup on destroy.  
- Process containment would rely on a small helper library (not implemented; see superseding ADR-007). External isolation tools could be added later without changing the provider contract.
- Preheating remains part of the contract: preheated resources materialize temp dirs and perform a lightweight health probe so pool semantics stay consistent.  
- Hook parity: support `on_provision` and `on_allocate` using `sh` on Linux and `pwsh` on Windows, with per-hook timeouts and output limits.  
- Initial mode is in-process execution; a future `mode: subprocess` (same binary helper) may be added for stronger isolation without changing the provider contract.  
- Surface a startup warning and docs clarifying that `runner` is for containment/dev/CI, not a hardened sandbox.  
- Register the provider in the existing registry under key `runner` and include it in CLI help (e.g., `boxy agent serve --provider runner`).

## Design Notes (high level)

- Resource = temp directory + command runner + log capture.  
- Status progression: Provisioned (temp dir created) → Ready (preheated/health checked) → Allocated (running hooks/commands) → Destroyed (processes killed, temp dir removed).  
- ConnectionInfo exposes local paths (temp dir, logs) and, if added later, an optional local socket for streaming output.  
- Limits: timeouts enforced via context; size caps for stdout/stderr; Linux uses ulimit/cgroups when available, Windows uses job objects; network deny is best-effort (future flags for nsjail/bwrap/WDAC).

## Consequences

### Positive

- Makes allocator/pool flows and hooks testable on any dev box or CI runner.  
- Provides a realistic lifecycle without requiring Docker/Hyper-V.  
- Keeps provider interface unchanged and compiled-in strategy intact.

### Negative

- Only containment, not hardened isolation; must document clearly.  
- Two OS backends increase implementation and test surface.  
- Preheating consumes some disk for idle temp dirs.
- Auto-detect adds a small risk of silent OS drift; optional config override mitigates.

## Alternatives Considered

- **“localproc”/“sandbox”/“exec” provider names**: rejected due to overload/ambiguity.  
- **Dropping preheating**: rejected to preserve pool contract symmetry.  
- **External plugin process**: rejected per ADR-002; compiled-in remains the approach.  
- **Strong isolation from day one (nsjail/bwrap/WDAC)**: deferred to keep MVP simple; will expose via future flags.
