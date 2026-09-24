# Global segment CIDR allocation (#370)

Status: accepted, implementing 2026-09-22.

## Problem

Per-sandbox network segments pick their own address range on each host, with
no cross-host coordination. Two hosts therefore hand out overlapping ranges,
which breaks cross-host mesh peering (#372) at the WireGuard layer.

Observed live on 2026-09-22 with two independent Docker daemons:

| | host A | host B |
|---|---|---|
| sandbox segment | `172.18.0.0/16` | `172.19.0.0/16` |
| host's own `docker0` | `172.17.0.0/16` | **`172.18.0.0/16`** |

Host B's own default bridge occupies the exact range host A's segment uses,
because both daemons allocate from the same default pool in the same order.
The WireGuard handshake completes, then every data packet is dropped:

```
IPv4 packet with disallowed source address from peer(OhQ/…1wTA)
```

That is cryptokey routing rejecting the decrypted inner packet — the source
address is not inside the `allowed_ips` set that is valid for that peer. It
happens *after* decryption, inside WireGuard, so it cannot be worked around
by fixing up `ip route` (attempted and confirmed ineffective in an earlier
session).

Both providers allocate host-locally today:

- **Hyper-V** carves `/29`s from `10.250.0.0/16` via a per-host `diskjson`
  ledger (`network-segments.json`). Two hosts each start at index 0, so both
  assign `10.250.0.0/29` to their first sandbox.
- **Docker** sets no IPAM at all and lets the daemon auto-assign, which is
  what produced the collision above.

## Two distinct collision types

These are separate problems and a fix must handle both:

1. **Cross-host collision** — two hosts choose the same range for different
   segments. Breaks mesh peering. Requires a global allocator.
2. **Host-local collision** — a range is already in use on one host by
   something Boxy did not create: `docker0`, a VPN, the corporate LAN, an
   unrelated bridge. Requires local knowledge the daemon does not have.

Docker's auto-IPAM solves (2) and causes (1). A naive global allocator would
solve (1) and reintroduce (2).

## Decision

**The daemon proposes, the agent verifies, the daemon retries.**

### 1. The daemon allocates, from state it already has

`model.NetworkSegment` gains a `CIDR` field. Since every segment is already
persisted on its sandbox (`model.Sandbox.NetworkSegments`), the set of
in-use CIDRs is derivable by scanning sandboxes — no new ledger, no new
store methods, and no separate lifecycle to keep in sync.

This deliberately avoids a standalone allocation ledger. The Hyper-V
per-host ledger is being deleted by this change precisely because a parallel
record of allocations can drift from the segments that actually exist; a
derived view cannot. Release is likewise automatic: deleting a sandbox
removes its segments, which frees their CIDRs.

Cost is a `ListSandboxes` scan per segment creation. Sandboxes are bounded
(tens to hundreds) and this is not a hot path — it runs once per
(sandbox, agent) pair, not per packet or per resource.

Allocation carves `/29`s from `10.250.0.0/16`, the base ADR-0021 already
chose for Hyper-V and reasoned about: clear of Docker's defaults (`172.17+`)
and of the `10.0.x`/`10.1.x` end of RFC 1918 where operator LANs usually
sit. `/29` is 8 addresses (network, gateway, up to 5 hosts, broadcast) and
`/16 ÷ /29` gives 8192 segments.

### 2. The agent verifies locally and can refuse

`NetworkIsolator.CreateSegment` takes the proposed CIDR:

```go
CreateSegment(ctx context.Context, sandboxID string, cidr string) (SegmentRef, error)
```

A driver that cannot use the proposed range returns
`providersdk.CIDRConflictError`, which reports what it collided with. This
follows the existing typed-error pattern (`CapacityError`,
`OrphanedResourceError`): it implements `ErrorTyper`, so it round-trips
across the RemoteAgent/gRPC boundary through the generic machinery already
in `remoteclient.go` (marshal) and `remote.go` (`reconstructAgentError`).
Only a new decode case is needed.

Local checks are provider-specific because the collision sources are:

- **Docker** — existing networks' IPAM subnets, plus host routes.
- **Hyper-V** — existing `Get-NetNat` prefixes and host IP addresses.

### 3. The daemon retries with the next block

On `CIDRConflictError` the caller advances to the next candidate `/29` and
re-calls, bounded by a retry cap. Refusals are **not persisted**: a
permanent local conflict costs a few wasted round trips on each future
allocation and self-corrects, whereas a persisted denial list is more stale
state of exactly the kind this design is removing. If the cap is exhausted,
segment creation fails loudly rather than proceeding onto a colliding range.

## Consequences

- `NetworkIsolator.CreateSegment` changes signature; both drivers and the
  agent wire protocol (`CreateSegmentCommand.cidr`) change with it. There is
  no compatibility shim — per this repo's "single source of truth, avoid
  parallel old vs new models" posture, the old self-allocating form is
  removed rather than kept as a fallback.
- Hyper-V's `segmentLedger` stops being an allocator. It remains only as the
  per-host record of what a sandbox's switch/CIDR/addresses are, which
  `AttachToSegment` needs for in-guest addressing.
- Docker moves from auto-IPAM to an explicit subnet. This is the change that
  reintroduces host-local collision risk for Docker, which is exactly why
  step 2 exists.
- Cross-host mesh peering becomes correct by construction rather than by
  luck of the two hosts' allocation order.

## Out of scope

- Reclaiming CIDRs from sandboxes that leaked their segment records. The
  existing segment-teardown gaps are tracked in #371.
- Operator-configurable base range. `10.250.0.0/16` stays a constant until
  there is a concrete deployment that needs otherwise.
