# Design: preserve guest command exit codes at the CLI boundary (#269)

## Scope

The REST execution contract already returns a guest exit code in buffered
responses and streaming completion events. The CLI currently turns every
non-zero guest result into an untyped Go error, and `cmd/boxy/main.go` then
collapses that error to process status 1.

## Decisions

- `internal/cli.ExitCodeError` carries the non-zero guest exit code and keeps
  the existing human-readable `command exited with code N` message.
- Buffered and streaming `sandbox exec` return this typed error for a guest
  exit; stream decode errors, HTTP failures, timeouts, and transport failures
  remain ordinary errors and therefore retain process status 1.
- `cmd/boxy/run()` uses `errors.As` through the CLI helper to return the typed
  guest code. It prints the typed guest error once as well, preserving the
  human-readable diagnostic while returning the guest status.

## Acceptance

- A buffered guest exit of 7 makes the process exit 7.
- A streaming guest exit of 23 makes the process exit 23.
- A non-zero guest exit prints `command exited with code N` before returning
  the guest status.
- Transport/client failures do not accidentally become guest exit codes.
