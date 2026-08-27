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
- `pkg/psdirect`'s `Exec.ExecStream` is uncovered by tests, deliberately, and
  the gap is specific to PSRP's API shape rather than a broader streaming
  blind spot: SSH (`pkg/vmsdk`), Docker, and devfactory streaming all have
  real `ExecStream`/`UpdateStream` test coverage. `psrpStreamExecutor
  .ExecuteStream` returns a concrete `*psrpclient.StreamResult` (not an
  interface) whose `Wait()`/`Cancel()` are methods on that struct with
  unexported fields (`pipeline`, `cleanup`) that panic on a nil receiver —
  there is no way to construct a working test double for it from outside
  the `go-psrp/client` package. Covering it would require introducing a
  local interface in `psdirect.go` that wraps `Wait`/`Cancel` so a mock can
  implement it — a production seam change, not a test addition, and
  deliberately not made during the 2026-08 exec-streaming hardening pass.
  As of the #247 fix below, everything in `ExecStream`'s per-item loop
  *except* that fan-in/`Wait`/`Cancel` orchestration is unit-tested: the
  per-value formatting, exit-marker handling, and newline-separator logic
  were extracted into `streamEmitter`/`newlineTracker`, both driven directly
  against a fake `eventstream.Sink` in tests. Only the outer orchestration
  around a real `*psrpclient.StreamResult` remains uncovered.

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
- **2026-08-12**: Code review on the PR that landed the above two entries
  surfaced two further real bugs before merge:
  - `boxy sandbox exec` used the default CLI HTTP client, whose
    `http.Client.Timeout` (5s) bounds the *entire* request including
    reading the response body — so any exec running longer than 5s (the
    common case, given the server's own default is 30s and its max is 5m)
    would have the client abort the request, or truncate a `--stream`
    response mid-output. Fixed with a dedicated `execAPIClientForServer`
    (5m, matching the server's `maxExecTimeout`), the same pattern already
    used for `debug pool drain/fill`'s `maintenanceAPIClientForServer`.
  - The prior TOCTOU fix's `deliver()` only raced its send against
    `a.closed` (whole-connection teardown), but a single `UpdateStream`
    call can give up on its own — its exec context expires, or its sink
    errors — entirely independently of connection health. With `a.closed`
    never closing in that case, `deliver()` could still block forever on a
    full `streamPending` buffer even though the connection was healthy,
    wedging `Serve()`'s single receive loop (and every other command
    routed to that agent) with no automatic recovery short of an operator
    running `boxy agent revoke`. Fixed by giving each `UpdateStream` call
    its own `streamWaiter{ch, done}`, with `done` closed by that call's
    deferred cleanup on every exit path; `deliver()` now races its send
    against `a.closed`, `waiter.done`, and the send itself — matching the
    three-way parity its removed comment incorrectly claimed it already
    had. See `pkg/agentsdk/remote.go`'s `streamWaiter`/`deliver`/
    `UpdateStream` and `internal/cli/api_client.go`'s
    `execAPIClientForServer`.
- **2026-08-12**: A GitHub Copilot review on the same PR (#162) caught a
  third gap: `pkg/agentsdk/remoteclient.go`'s `remoteStreamSink` — the
  agent-side `eventstream.Sink` that forwards a driver's stream events back
  to the server — read and wrote its `completed bool` with no
  synchronization, including a direct field read from
  `executeStreamingCommand` that bypassed even the type's own `Send`/
  `complete` methods. No concrete driver in this codebase calls `Send`
  concurrently (every one funnels through a single consumer goroutine even
  when it fans work out to producer goroutines internally, e.g.
  `pkg/vmsdk/ssh.go`'s independent stdout/stderr readers), so this was
  never reachable via code actually wired up here — but
  `providersdk.StreamingDriver` is a public interface, and nothing in
  `eventstream.Sink`'s contract promises `Send` is single-goroutine-only.
  Fixed with a mutex guarding `completed` and a synchronized `isCompleted()`
  accessor for the one external read; covered by
  `TestRemoteStreamSink_ConcurrentSendDoesNotRaceOrDoubleComplete`, which
  drives concurrent `Send` calls and is checked under `-race` in CI (not
  available on windows/arm64 dev hosts).
- **2026-08-26**: Fixed two silent-corruption bugs a QA bot found in
  `pkg/psdirect`, #238 and #239: `psQuote` only escaped embedded `'` for the
  PowerShell parser, but Windows PowerShell 5.1's `&` call operator does its
  own native-argument-line reconstruction for native executables that does
  not preserve embedded `"` (verified live against a round-trip matrix,
  including a case where the issue's own suggested minimal fix is
  empirically wrong, not just incomplete — see `escapeNativeArg`'s doc
  comment); and `extractOutput` concatenated PSRP stream items with no
  separator and could render a non-string item as the literal token
  `"PSObject"`, discarding real content. Fixed with a new `escapeNativeArg`
  (called from `psQuote`) and a new `formatStreamValue` helper shared
  between `extractOutput` and `ExecStream`'s per-item loop. This **partially**
  closes the streaming test-coverage gap noted above: per-item output
  *formatting* (`formatStreamValue`) is now unit-tested without needing a
  `StreamResult` double, but `ExecStream`'s orchestration (fan-in goroutines,
  `Wait()`/`Cancel()`, context-cancellation races) remains uncovered for the
  same reason — no constructible test double for `*psrpclient.StreamResult`
  from outside `go-psrp/client`.

  `escapeNativeArg` is a documented stopgap, not the intended long-term fix.
  The real fix is to stop reconstructing a text command line at all:
  `go-psrpcore`'s `pipeline.Pipeline` already supports invoking a command via
  `AddCommand`/`AddArgument` — PSRP's native equivalent of a parameterized
  query, which this entire bug class cannot occur against — but this
  package's `go-psrp` client dependency doesn't currently expose that at the
  client level. Tracked in #244 (either an upstream contribution to
  `go-psrp` or a `replace`-directive fork adding the client-level API).
- **2026-08-27**: Fixed #247, the unfixed half of #239: #239's separator
  logic landed in `extractOutput` (used only by the non-streaming `Exec`
  path), but `ExecStream` — the loop both public exec APIs actually go
  through (`internal/server/api_exec.go`'s `handleBufferedExec` for plain
  `sandbox exec`, and the CLI's `--stream` renderer) — never received it,
  so any multi-line command output still came back as one run-on line with
  no line breaks. Fixed by extracting the separator logic into a shared
  `newlineTracker` type used by both `extractOutput` and a new
  `streamEmitter` (which now holds `ExecStream`'s per-value formatting,
  exit-marker detection, and send logic, one `newlineTracker` per output
  channel since stdout/stderr are consumed as independently concatenated
  streams and stderr itself merges several underlying PSRP streams —
  Errors, Warnings, Verbose, Debug, Progress, Information). Also fixed a
  latent bug caught during design review before it shipped: computing the
  next call's separator decision from the *already-prefixed* text (instead
  of the original item text) would have silently dropped a real blank line
  whenever an empty stream item appeared mid-stream. See
  `pkg/psdirect/psdirect.go`'s `newlineTracker`/`streamEmitter` and the
  coverage note above for how this also extended, but did not close, the
  `ExecStream` test-coverage gap.
