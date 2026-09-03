# Large-release batch lessons learned

This batch combined package dependency graphs, S3-compatible artifacts, UI improvements, structured diagnostics, agent log ingestion, and the #315 pool-filling investigation.

## What worked

- Deferring expensive integration, race, browser, and smoke validation until focused implementation and unit tests were complete kept the feedback loop small and the final validation meaningful.
- Quiet `agent:` Taskfile wrappers made exit status the default signal and limited captured output to bounded failure diagnostics. Coverage needed direct wrapper invocation because nested Taskfile environment handling did not reliably propagate `GOFLAGS`.
- Structural Go discovery with `ast-grep`/`gopls`, plus `yq`/`jq` projections for configuration and reports, avoided repeatedly loading large source files or command transcripts.
- Regenerating protobufs and API documentation from their source definitions caught drift between implementation, generated code, and published docs.

## Safety and supportability

- Agent identity is bound by the authenticated server session rather than accepted from log payloads. Diagnostic exports are bounded and sanitized by default, with stable placeholders for correlating events without exposing hostnames, usernames, credentials, URLs, IPs, or resource identifiers.
- Provider logs retain structured operation, error code, and safe summary fields. The Hyper-V investigation found insufficient evidence for a broader admission change; a bounded transient-memory-probe retry was the justified provider fix, while unknown capacity remains fail-closed.
- Dev Factory stubs provide a practical path for smoke and source-ingestion validation when Windows Hyper-V is unavailable. Hyper-V guest validation remains an environment-dependent gate.

## Follow-up guidance

- Keep source/store registration declarative in `boxy.yaml`; keep signed URLs ephemeral and provider-facing, never in persistent resource properties or the control-plane archive.
- When adding new diagnostics producers, emit through the shared safe event contract and attach a bounded `diagnostics.Shipper` to remote agents. Add a component-specific export contract test alongside the producer.
