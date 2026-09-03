# Diagnostics export and agent log shipping

## Contract

Boxy exposes a bounded, administrator-only diagnostics export. The export is
JSON so it can be attached to an issue or passed to another troubleshooting
tool without requiring Boxy-specific storage access. Export construction always
sanitizes event text and identifiers; there is no raw-export switch.

The public `pkg/diagnostics` package owns the export schema, component manifest,
sanitization contract, and bounded log shipper. Components decide which safe
events they emit and identify themselves with a component name. The server
registers the control-plane and agent components; providers may add more
component descriptions without changing the archive format.

Agents may attach the shipper to their `slog` handler. It buffers a bounded
number of already-safe events, sends bounded batches over the authenticated
agent stream, and retains a failed batch for retry. The server accepts only
authenticated agent log batches and stores them as server-observed diagnostics.
The control plane remains the source of truth for export assembly; the agent is
only a driver/transport host.

## Safety and limits

- Exports contain at most the diagnostics hard limit (1,000 events).
- Credentials, bearer values, signed URLs, query secrets, hostnames, IP
  addresses, usernames, and agent/resource identifiers are redacted or mapped
  to stable placeholders within one export.
- Export metadata identifies the schema version, generation time, component
  descriptions, and whether sanitization was applied.
- Agent batches are bounded by queue and batch limits and never include raw
  `slog` attributes outside the diagnostics allowlist.
- Agent log submission is best effort: a full queue or failed transport does
  not take down provider operations.

## Workflow

1. A component logs through the diagnostics handler or submits a safe event to
   the public shipper.
2. An agent flushes one bounded batch over its existing authenticated stream.
3. The server validates the authenticated agent identity, appends events with
   that identity, and applies normal retention.
4. An administrator runs `boxy diagnostics export`, optionally filtering by
   time, pool, agent, resource, or component.
5. The resulting sanitized JSON can be attached to an issue without copying
   credentials or machine identity from the host.

