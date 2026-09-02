# Durable sandbox execution

## Status

Accepted for the next prerelease batch (#243 and #260).

## Contract

`POST /api/v1/sandboxes/{id}/exec` validates the sandbox, selects one
allocated resource, atomically claims that resource, persists a safe execution
record, and returns without waiting for the provider:

```json
{"exec_id":"...","status":"running"}
```

The durable record contains only the execution, sandbox, resource, actor and
request-fingerprint metadata, lifecycle timestamps, input kind, bounded output
chunks, truncation state, exit code, and a safe error string. It never contains
credentials, environment values, script bytes, or command arguments.

`GET /api/v1/sandboxes/{id}/exec/{exec_id}?from=<cursor>` returns a bounded page
of output chunks and the execution status. A cursor is an opaque bookmark that
lands between chunks. Reusing the returned cursor does not return chunks that
were already acknowledged. Output is capped at 1 MiB; if the provider writes
past the cap, the record retains an explicit truncation marker and completes
with the output-limit error. Terminal records are retained for 24 hours.

`POST /api/v1/sandboxes/{id}/exec/{exec_id}/cancel` requests cancellation. The
worker uses a server-owned context, so disconnecting a reader does not cancel
the execution. A daemon restart marks pending/running records `interrupted`
and never replays them.

Only one execution may run against a resource. A competing submission returns
`409` with an error code of `resource_busy` and the active `exec_id`; requests
against different resources continue concurrently. There is no queue or
implicit replay.

The CLI keeps positional command and script-file forms, and adds `--command`
for one opaque command string and `--stdin` for reading that opaque command
from stdin. These forms are exclusive with one another. `--detach` prints the
execution ID, while `--attach <exec_id>` tails an existing record. The default
client behavior tails output and exits with the provider exit code; `--events`
prints the structured event stream and `--buffered` waits for terminal output.

## Failure and authorization rules

The POST authorization and sandbox ownership rules apply to GET and cancel.
Unknown executions, executions belonging to another sandbox, and unauthorized
users are not disclosed. Nonzero guest exit codes are terminal command results,
not control-plane failures. Provider failures, cancellation, timeout, and
interruption are represented in the terminal record and event stream.

## Testing scenarios

The test matrix covers delayed chunks, reconnect without duplicates, cursor
truncation, busy responses, different-resource concurrency, cancellation,
restart interruption without replay, retention, authorization, input
exclusivity, and nonzero exit codes. Devfactory scenarios remain deterministic
and provider-neutral.
