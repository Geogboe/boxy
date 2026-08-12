# ADR-0008: Streaming sandbox command execution

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

Sandbox command execution must provide live output, not merely return a final
buffer after the provider exits. Boxy's current provider `Update` result and
remote-agent gRPC operation result are unary, so adding an HTTP chunked response
alone would not provide true streaming.

## Decisions

- #153 includes true end-to-end output streaming in the next prerelease.
- The reusable stream vocabulary and lifecycle primitives belong in a public
  package (`pkg/eventstream`) rather than in REST or a provider driver.
  The package contains only provider-neutral output chunks, completion, exit
  code, cancellation, and bounded backpressure/error contracts. It does not
  implement Docker/Hyper-V execution, REST handlers, gRPC adapters, or other
  Boxy-specific behavior; those remain in the owning packages.
- The provider SDK gains an optional streaming execution capability. Existing
  unary `Update` remains valid for non-streaming operations and providers that
  cannot stream; the sandbox exec path returns an explicit capability error when
  streaming is unavailable rather than pretending a final buffer is live.
- The remote agent's existing bidirectional transport is extended with typed
  execution-stream messages correlated by command ID. Provider output must flow
  agent → server → REST client without logging or persisting guest output.
- REST exposes a streaming exec mode using a documented, line-delimited event
  format. The CLI renders live stdout/stderr while retaining a structured final
  completion event. Interactive stdin/PTY support remains out of scope.
- The strict safety rules remain: a single-resource sandbox may target that
  resource implicitly; a multi-resource sandbox requires an explicit resource
  ID; execution has a default timeout, a hard maximum, bounded output, and
  cancellation on client disconnect.

## Consequences

The REST stream uses one JSON object per line. Data payloads are base64 encoded
so arbitrary binary output cannot corrupt framing; completion carries provider
attributes such as `exit_code`. The non-streaming REST response remains a
bounded JSON convenience for scripts.

- The protobuf schema and generated Go stubs must be regenerated with the pinned
  Buf task after protocol changes.
- Docker, Hyper-V's SSH and PowerShell Direct guest paths, and the devfactory
  reference driver provide streaming adapters. Hyper-V returns an explicit
  capability error when a custom guest executor does not implement the optional
  streaming interface; other providers may opt in later.
- REST, CLI, bundled skill, API catalog, generated API docs, and tests must be
  updated together. This is intentionally larger than a single handler change.

## Change notes

- **2026-08-11**: Fixed a TOCTOU race in `agentsdk.RemoteAgent`: `Close()`
  used to close each pending/streaming command's response channel while
  `deliver()` (running on the `Serve()` goroutine) could concurrently read
  that same channel reference and send on it — a live path, since
  `Server.Revoke` calls `remote.Close()` from a different goroutine than
  `Serve()`. Closing `a.closed` already unblocks every waiter via their
  `case <-a.closed` select arm, so `Close()` now only clears the maps and
  never closes the per-command channels; a late send from `deliver()` just
  lands in an unread, eventually-garbage-collected buffer instead of
  panicking. Also decided and documented the buffered-mode output-limit
  contract left open above: exceeding the bounded response limit (1 MiB
  total) in the default (non-streaming) mode returns `413 Payload Too
  Large` and discards whatever partial output was collected, distinct from
  the `504` timeout and `500` generic-provider-failure cases; streaming mode
  needs no new status handling since output already reached the client
  incrementally; the limit violation there ends the stream as a terminal
  `complete` event carrying the error instead. See
  `internal/server/api_exec.go`'s `writeExecError` and
  `pkg/agentsdk/remote.go`'s `Close`/`deliver`.
