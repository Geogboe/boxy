# On-demand remote agent logs (#313)

## Goal

An administrator can ask a connected remote agent for its local diagnostic
events from the CLI or web UI. Agents do not continuously transmit their log
history. The first request may ask for the bounded retained history; later
requests pass an RFC3339 `since` value to retrieve only the diff.

## Contract

- `boxy diagnostics collect --agent <id>` requests the agent's retained
  diagnostics and reports the request ID. `--since` narrows the request to
  events newer than that timestamp; `--limit` remains bounded by the public
  diagnostics hard limit.
- `POST /api/v1/agents/{id}/logs` is administrator-only and returns `202` with
  the request ID. The agent response is stored in the server diagnostics store
  under the authenticated agent identity.
- The diagnostics page exposes the same action for the selected agent and
  keeps the existing redacted query/export workflow.
- Agent-local diagnostics are stored below the agent data directory with a
  default retention of 14 days and the existing bounded byte limit. The
  server's remote events use the same default retention and can be overridden
  by configuration.
- The request and response are bounded, sanitized, and best effort. A missing
  local store yields an empty completed response; a disconnected agent returns
  a service-unavailable error without changing server state.

## Transport

The authenticated bidirectional stream gains a server-to-agent `LogRequest`
message carrying a request ID, optional Unix-nanosecond lower bound, and event
limit. The agent reads its local diagnostics store and returns one bounded
`LogBatch` carrying the request ID. Existing heartbeat log shipping remains
disabled unless explicitly configured, so this feature is a pull operation.

## Test-first acceptance cases

1. An agent returns only events after `since`, never raw slog attributes, and
   preserves the request ID.
2. The server stores a pulled batch with the authenticated agent ID even when
   the payload attempts to supply another ID.
3. The API rejects non-admin requests, invalid timestamps, and limits outside
   the diagnostics hard bound.
4. The CLI sends the request, prints the request ID, and validates `--since`
   and `--limit` before making an HTTP request.
5. Agent and server stores apply 14-day default retention and continue to
   enforce the existing byte/event bounds.
