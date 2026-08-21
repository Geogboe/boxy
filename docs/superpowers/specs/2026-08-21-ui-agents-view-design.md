# Agents Dashboard with Heartbeat and Capacity Detail

## Purpose

Add a read-only `/ui/agents` dashboard that makes the distinction between
agent transport liveness and provisioning eligibility visible. The view lists
the embedded local agent and every remote agent still registered with the
server, including remote agents that have disconnected.

## API contract

`GET /api/v1/agents` keeps its existing authentication and path. Its response
retains `id`, `name`, `providers`, and `available`, and adds:

- `connected` — whether the embedded agent is active or the remote agent has
  an active authenticated transport.
- `last_seen` — optional RFC3339 UTC server-receipt time for the latest remote
  heartbeat. It is absent for the embedded agent and before a remote agent has
  sent its first heartbeat.
- `availability` — optional object keyed by registered provider type. Each
  value contains the provider's reported `memory_mb` capacity.
- `availability_at` — optional RFC3339 UTC server-receipt time for the latest
  availability sample. A heartbeat with no successful capacity entries still
  updates this timestamp and leaves `availability` empty.

The new fields are additive within `/api/v1`; existing clients may ignore
them. Provider values are rendered by the order in `providers`, never by map
iteration order.

## Status semantics

`connected` and `available` are independent:

- An active remote transport can be connected but unavailable after missed
  heartbeats.
- A disconnected remote agent remains listed, with `connected: false` and
  `available: false`.
- The embedded agent is connected in-process and has no heartbeat or
  availability sample.
- `last_seen` is based on server receipt time, not an agent-provided clock.
- Capacity is optional. A provider missing from the latest sample means there
  is no capacity sample, not zero capacity.

## Page behavior

`/ui/agents` uses the existing dark dashboard palette and system typography. A
paired Connection/Scheduling status cluster is the signature element of each
row; text labels and `aria-label`s keep the distinction clear without relying
on color. The table includes the agent name and ID, provider names and memory
capacity, heartbeat/sample timestamps in UTC, and explicit “No heartbeat
sample” and “No capacity sample” states.

The table fragment is refreshed by HTMX every five seconds at
`/ui/fragments/agents-table`. Empty inventories and loader failures have
visible states consistent with the other dashboard pages. The page is
responsive and read-only.

## Scope exclusions

This change does not add revoke controls, agent detail drill-downs, scheduler
changes, persistence, new protobuf fields, or changes to the existing
`boxy agent list` output.
