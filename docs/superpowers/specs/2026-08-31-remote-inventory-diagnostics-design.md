# Design: remote inventory consistency and diagnostics (#294, #293)

## Inventory incident

The Hyper-V provider currently serializes one VM per line. PowerShell Direct
can deliver a multiline string with its line separators removed, so two valid
VMs can arrive at the control plane as one malformed record. A parser that
silently skips malformed records then gives reconciliation an incomplete view;
reconciliation can mark the omitted resources destroyed and pool allocation
loses them from ready inventory.

The provider listing wire format is therefore a compact JSON array. A listing
is either a complete, valid snapshot or an error. Reconciliation fails closed
for an error and never reaps resources from an unverifiable provider snapshot.
Resource IDs remain the provider's canonical IDs and are compared unchanged
across create, list, store, allocation, and the Hyper-V IP ledger.

The devfactory reference provider has an explicit `fail_list` test knob. It
returns a listing error without changing normal behavior, allowing local
reconciliation and remote-agent tests to exercise this fail-closed path on a
small laptop without a Hyper-V host.

## Diagnostics scope

The daemon exposes control-plane logs and server-observed remote-agent errors;
it does not add a remote-agent log-forwarding protocol in this release. Logs
are persisted in a bounded, redacted JSONL file below the daemon data
directory. The default retention is seven days or 10 MiB, whichever is
reached first. The store is append-only from the handler's perspective and
uses a temporary-file replacement when compacting or rewriting state.

Each diagnostic record contains only timestamp, level, component, message, and
approved correlation fields (`pool`, `agent`, `resource`, and `request`).
Authorization values, API keys, credentials, registration tokens, URI
userinfo/query secrets, raw headers, commands, guest connection details, and
provider payloads are removed or replaced before persistence.

`GET /api/v1/diagnostics/logs`, `boxy diagnostics logs`, and `/ui/diagnostics`
are administrator-only. Queries are bounded to 100 records by default and
1,000 records at the hard maximum, ordered newest-first, and use opaque
cursors. Query audit records go through a separate safe audit sink so a query
cannot recursively create diagnostic records.

Diagnostics are historical and non-streaming. Normal stderr/`--log-file`
logging remains unchanged. A daemon restart reloads the bounded file; an
agent's local process log remains outside this endpoint.
