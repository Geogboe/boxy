# Streaming-first sandbox execution

## Status

Accepted for implementation as the streaming slice of #260. Same-resource
execution serialization and queueing are intentionally deferred to a separate
follow-up.

## Goal

Make long-running sandbox commands useful immediately: users should see guest
output as it arrives instead of waiting for the command to finish. The HTTP
API and CLI should share one streaming execution contract while retaining an
explicit buffered mode for callers that need one final JSON document.

## Contract

### HTTP API

`POST /api/v1/sandboxes/{id}/exec` defaults to
`Content-Type: application/x-ndjson`. Each data event carries the existing
base64-encoded payload and stream name. The final `complete` event is
authoritative and carries the exit code on guest completion or an error on
transport/provider failure.

`?stream=true` is an explicit spelling of the default. `?stream=false`
selects the existing bounded buffered JSON response. Buffered mode keeps its
current output limit and HTTP error mapping. Once a streaming response has
started, failures are represented in the final event because HTTP status
cannot be changed after headers are sent.

The server keeps the existing context deadline, output limits, cancellation,
backpressure, and completion behavior. This change does not claim to make
concurrent commands against one resource safe or queued.

### CLI

`boxy sandbox exec <id> -- <command> [args...]` consumes the default HTTP
stream and writes decoded stdout/stderr bytes as they arrive. The command
returns a typed guest exit-code error after the completion event when the
guest exits nonzero.

`--events` writes the structured NDJSON event records for automation.
`--buffered` requests the buffered JSON response and preserves the
wait-until-complete behavior. The old `--stream` flag is replaced by these
explicit modes; documentation and tests must not continue to present it as
the normal user path.

## Implementation shape

- Change the server's default mode selection while keeping `stream=false`
  as the explicit buffered branch.
- Split CLI response handling into live human output, structured event
  output, and buffered response handling without duplicating credential,
  timeout, or error plumbing.
- Preserve separate stdout/stderr routing for human output and preserve the
  current event schema for structured output.
- Add a deterministic multi-chunk execution fixture in Devfactory or the
  shared test seam so tests can prove output is observable before completion
  without requiring a Hyper-V guest.

## Verification

Focused tests must cover default HTTP streaming, explicit buffered HTTP
responses, CLI live output, CLI event output, CLI buffered output, completion
errors, nonzero guest exit codes, cancellation, output limits, and transport
failures. Run the repository's full validation, WSL race helper, serve E2E,
and local `act` checks where supported. There is no new browser surface; use
Firefox Playwright only for the existing UI regression smoke if that smoke is
run as part of the release gate.
