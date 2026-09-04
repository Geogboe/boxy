# Pool reliability, jobs, and diagnostics release

## Goals

This release makes pool maintenance durable and observable. Pool Fill, Retry,
Destroy, and remote-agent log pulls run as tracked jobs; Hyper-V capacity and
template identity are enforced before cloning; guest credentials survive
package retries; and the web UI presents one coherent pool-operation timeline.

The release intentionally replaces message-centric diagnostic events with a
structured step model. Boxy has no compatibility requirement for the old
diagnostics response shape, but credentials, command text, and provider-owned
payloads remain excluded from diagnostics and generic job records.

## Generic jobs

`pkg/jobs` owns only generic lifecycle concerns: identity, kind, target key,
status, timestamps, safe progress steps, cancellation, durable storage,
restart interruption, active-target exclusion, and fourteen-day retention. It
does not know about pools, VMs, providers, logs, command output, or cleanup
policy. Domain adapters supply handlers and safe progress codes.

Statuses are `pending`, `running`, `cancelling`, `succeeded`, `failed`,
`cancelled`, and `interrupted`. A target may have at most one active mutating
job. Cancelling stops the active operation and runs the domain cleanup hook;
the job becomes `cancelled` only after cleanup succeeds. Cleanup failure is a
terminal `failed` job with `cleanup_failed` as its safe error code. At process
startup, persisted pending, running, or cancelling jobs become `interrupted`.

Pool mutations and agent-log pulls return `202 Accepted` with a job ID. Generic
job read and cancel endpoints expose status and safe progress. Sandbox command
execution uses the same lifecycle engine, while its bounded stdout/stderr
chunks remain in the sandbox-specific execution adapter.

## Pool and Hyper-V behavior

Each Hyper-V provider configuration requires `memory_budget_mb`. The budget is
shared by every pool using that provider. Admission checks both remaining Boxy
budget and live Windows available memory after the existing host reserve; the
smaller value is advertised as availability. A temporarily insufficient live
sample is retried with bounded backoff and then refused without cloning.

Every VM template references a named source with a canonical SHA-256 digest.
Boxy verifies the source immediately before use and refuses provisioning if it
changed. Package-manager bootstrap is enabled by a template package request
and uses the package manifest's approved pinned version and immutable digest;
it never selects a host's latest version.

Admission creates and starts the VM, rotates its password once, persists the
new credential, then applies packages. Package application gets three total
attempts against the same powered-on VM and saved credential. After the third
failure, Boxy powers down and removes the VM by default, and a manual Retry
provisions a new resource from the template rather than reusing the torn-down
VM or its (now-deleted) credential. An opt-in, local-config-only pool policy,
`policy.debug.retain_failed_resources`, changes this: when set, admission
failures leave the VM running and its guest credential intact instead of
tearing it down, and a manual Retry against a retained failed resource
re-admits it in place, reusing the same VM and credential. This is
troubleshooting-only — a retained failed resource still consumes `max_total`
capacity and can make the pool `blocked` exactly like any other failed
resource. Failed resources that consume `max_total` without ready capacity
make the pool `blocked`; Fill must not report success in that state.

Only one mutating pool job may run at a time. While it runs, Fill, Retry, and
Destroy are unavailable; Cancel is the only mutation. Logs and Inspect remain
available. Destroy may remove a healthy warm resource, after which normal
reconciliation restores `min_ready`.

## Configuration and UI

Administrators can read and update pool settings through the API and a simple
web form. The deployed local configuration seeds these values and remains
authoritative when explicitly deployed again. Web/API edits persist across
ordinary restarts, use last-write-wins, and display a warning that a future
local-config push overwrites them. Provider, template, store, and secret
configuration remains local-file owned.

Updates are validated before persistence. Invalid input leaves the active and
persisted pool settings unchanged. Valid settings are saved even when their
provider is offline; they appear as pending until reconciliation can apply
them.

The Pools page shows one compact row per pool with `ready`, `filling`,
`draining`, `blocked`, or `unknown` status, readiness counts, hidden history,
and Retry, Logs, Copy ID, Inspect, and Destroy actions. HTMX refreshes job and
pool state without starting duplicate work.

## Diagnostics

Diagnostic events use stable codes and fields instead of prose messages. The
shape includes event ID, timestamp, level, component, operation, step, status,
attempt, job ID, pool, agent, resource, provider, error code, and a short safe
error summary. A Fill job is shown as one story with per-resource steps nested
inside it; a flat filtered/exportable table remains available.

The file store maintains a bounded newest-first cache and refreshes it when the
backing file changes, so repeated pages do not decode and sort the full JSONL
file. Retention and cursor behavior remain durable across restart and visible
across processes.

Remote-agent log pulls are jobs. The diagnostics page displays pending/running
state, refreshes until the authenticated agent batch arrives, then shows the
new events. Hyper-V personalization and availability failures emit agent-side
structured phase, operation, and safe error-code events.

## Validation

Unit, API, storage, UI, protocol, and integration tests cover lifecycle
recovery, cancellation cleanup, active-target exclusion, pool blocking,
password reuse, package retries, memory budgets, template mutation,
configuration precedence, diagnostics redaction, agent-log completion, and
cache invalidation. Browser validation uses Firefox.

The Hyper-V smoke test uses `wks01`, one VM maximum, 2 GB RAM, and the existing
Server Core 2025 template. The throwaway administrator credential is supplied
only at runtime and must never enter source, command output, or diagnostics.
