# ADR-0022: Cross-host mesh peering as an optional driver capability

## Status

Accepted (2026-09-16). Part of #224 (Decision 2).

## Context

ADR-0021 gives every sandbox its own per-host network segment, but a segment
is a host-local object (a Hyper-V Internal vSwitch, a Docker bridge) — two
segments on different hosts are separate, unconnected L2 networks. A sandbox
whose resources land on two hosts, because that is where capacity was, has
no way for those resources to reach each other. `docs/superpowers/specs/
2026-09-10-overlay-network-fabric-design.md` (Decision 2) is the full design
this ADR records; that document also covers JIT native-protocol access
(Decision 3) and the `AccessBroker` extension point (Decision 4), which are
separate, not-yet-implemented plans and out of scope here.

The rejected alternative — a per-host embedded WireGuard client in the CLI
mirroring the agent-to-agent mesh, for interactive human access — turned out
to be a different problem (one person, one stream) better solved by relaying
through the existing control-plane connection, the same way `kubectl
port-forward` and Teleport/Boundary sessions do. That is Decision 3, not
this ADR.

## Decision

### A new optional capability, layered on `NetworkIsolator`

`providersdk.MeshPeerer` (`pkg/providersdk/mesh_peering.go`) is an optional
driver capability, discovered by type assertion exactly like
`NetworkIsolator` and every other optional capability in this package. It
operates on a `SegmentRef` a `NetworkIsolator.CreateSegment` already
produced — a driver cannot usefully implement one without the other.

```go
type MeshPeerer interface {
    MeshIdentity(ctx context.Context, ref SegmentRef) (publicKey, endpoint, cidr string, err error)
    AddMeshPeer(ctx context.Context, ref SegmentRef, peerPublicKey, peerEndpoint, peerCIDR string) error
    RemoveMeshPeer(ctx context.Context, ref SegmentRef, peerPublicKey string) error
}
```

`MeshIdentity` is the first call for a given segment: it lazily creates the
segment's WireGuard interface if one doesn't exist yet, and returns what a
remote peer needs — a public key, a reachable `host:port` endpoint, and the
segment's own subnet CIDR. `AddMeshPeer`/`RemoveMeshPeer` require
`MeshIdentity` to have already run for that segment at least once; calling
either first is a caller error, not something to silently no-op — the same
"idempotent, not forgiving of a skipped step" posture ADR-0021 already
establishes for `AttachToSegment`.

### `pkg/meshnet`: a provider-neutral `wireguard-go` primitive

`pkg/meshnet` owns the agent-side WireGuard device lifecycle
(`golang.zx2c4.com/wireguard`) — interface creation, keypair generation,
`AddPeer`/`RemovePeer`, teardown. It is pure Go with no host dependency
beyond what `wireguard-go`'s userspace TUN implementation needs, tested with
two loopback instances exercising a real peer handshake and data flow — no
live Hyper-V or Docker host required, unlike `NetworkIsolator`'s driver
implementations.

Each host a given sandbox actually touches gets its own WireGuard interface
**for that sandbox specifically** — a sandbox spanning 3 hosts means 3
interfaces, one per host, never one interface shared across different
sandboxes on the same host. This preserves the same "one sandbox's bug
can't leak into another's" property `NetworkIsolator`'s per-sandbox
switch/bridge already gives at the OS-object level. `MeshPeerer` is what a
driver implements to plug its segment's routing into `pkg/meshnet`; nothing
above the driver layer touches WireGuard directly.

**Key handling:** each agent generates its own WireGuard keypair locally,
per segment, on demand. The private key never leaves the agent process —
not sent to `boxy serve`, not persisted anywhere else. Only the public key
crosses the wire, as part of `MeshIdentity`'s return value.

**The reachable endpoint is operator-configured, not auto-detected** — a
host can be multi-homed, and guessing which address is reachable by other
Boxy hosts is exactly the kind of inference this project avoids elsewhere
(`hyperv.Config.DataDir`, ADR-0012/ADR-0013's explicit-over-inferred
posture).

### Peer introduction rides the existing ADR-0005 transport

`boxy serve` already holds an authenticated gRPC connection to every agent
(mTLS, per-agent identity, ADR-0005). When a sandbox's resources span
Host-1 and Host-2, `boxy serve` collects each host's `MeshIdentity` result
and relays Host-2's public key/endpoint/CIDR to Host-1's agent via
`AddMeshPeer`, and the mirror to Host-2's, over that existing channel — no
new transport, no extra registration step. Each agent's `pkg/meshnet`
component then peers directly with the other; the encrypted tunnel itself
is host-to-host, direct UDP — `boxy serve` is never in that data path, only
in the introduction. This is architecturally the same role Headscale plays
for Tailscale (coordinator, not relay); Boxy already has the coordinator.

### Wiring: full pairwise mesh, sandbox-scoped, triggered at allocation

`internal/sandbox`'s allocation path triggers mesh peering only when a
sandbox's claimed resources actually span more than one host/agent. A
single-host sandbox never touches `pkg/meshnet` or `MeshPeerer` at all —
WireGuard peer count scales with concurrent *multi-host* sandboxes, not
total sandbox count. When a sandbox does span N hosts, it gets a full
pairwise mesh among exactly those N agents' segments (N·(N-1)/2 peer
pairs), nothing global — no cross-sandbox connectivity, no mesh member
outside that one sandbox's own claimed hosts.

Wired end-to-end: `agentproto` gained `MeshIdentity`/`AddMeshPeer`/
`RemoveMeshPeer` wire messages; `agentsdk.MeshPeeringAgent` is implemented
by both `EmbeddedAgent` (type-asserts the local driver) and `RemoteAgent`
(three new gRPC commands over the existing stream), mirroring
`NetworkIsolatingAgent`'s shape from ADR-0021; `hyperv.Driver` and
`docker.Driver` both implement `MeshPeerer`. `devfactory` deliberately does
not, same reasoning as `NetworkIsolator`/`GuestPersonalizer` above — one
real consumer per platform, no second provider to validate a simulated
contract against.

## Consequences

- A sandbox that ends up spanning hosts pays a real cost per host pair: a
  WireGuard handshake and an `AddMeshPeer` round trip through `boxy serve`
  to each other host in the sandbox, on top of `NetworkIsolator`'s existing
  per-resource `AttachToSegment` cost (ADR-0021's 2026-09-14 entry already
  notes that method is heavier than its "lightweight reconnect" framing
  once guest addressing is included). This compounds #350 (per-sandbox
  timeout does not scale with resource count) further for multi-host
  sandboxes specifically.
- `pkg/meshnet` is unit-tested against real loopback WireGuard instances,
  which is stronger coverage than `NetworkIsolator`'s driver
  implementations get (fakes only) — but the *driver* implementations of
  `MeshPeerer` (`hyperv.Driver`, `docker.Driver`) are fake-executor-tested
  only, same as `NetworkIsolator`, and share its "unverified against live
  infrastructure from this development host" caveat (see AGENTS.md).
- `#224` stays open even though Decisions 1 (ADR-0021) and 2 (this ADR) are
  both now implemented: Decision 3 (JIT native-protocol access) and
  Decision 4 (`AccessBroker` extension point) are separate, unimplemented
  plans within the same design document.

## Open risks

Real-hardware validation (wks01, 2026-09-16) confirmed `NetworkIsolator`
end-to-end for the single-host case (see ADR-0021's Open Risks section),
but could not reach cross-host `MeshPeerer` validation. The attempt used two
`boxy agent serve` processes on the *same* physical host (no second machine
was available) as a stand-in for two hosts, and hit a real topology limit
before mesh peering itself was exercised: `queryBoxyMemoryMB` sums
host-wide rather than per-agent, and orphan-adoption in each agent's
resource-listing reconciliation has no per-agent ownership filter, so both
agents ended up reconciling each other's VMs as orphans (switches and NAT
are also host-global, compounding this). This is a real gap in two-agents-
sharing-one-host support, not a mesh-peering defect, but it means:

1. **Cross-host `MeshPeerer` behavior was unverified against live
   infrastructure — now partially resolved for Docker, see the 2026-09-18
   change-log entry below.** Two independent Docker daemons proved the
   `boxy serve` introduction relay and the `AddMeshPeer`/WireGuard handshake
   path for real, and found three real bugs along the way (all fixed).
   Packet-level connectivity itself was not conclusively proven (test-harness
   network collisions, not a known product bug). **Hyper-V's `MeshPeerer`
   remains entirely unverified** — this still needs a genuine second
   physical Hyper-V host.
2. **Two agents sharing one physical host is not currently a supported
   topology**, independent of mesh peering — `queryBoxyMemoryMB` and
   orphan-adoption both need to become agent-scoped before it would be.
   This was discovered as a side effect of trying to test Decision 2 on
   single-host lab hardware, not something Decision 2's design assumed;
   the design itself assumes hosts are distinct, which is the normal case
   in production.
3. The `New-NetNat` one-instance-per-host risk ADR-0021 records as "STILL
   OPEN, unverified" remains open for the same reason — the two-agent test
   setup was intended to also exercise a second concurrent `New-NetNat` on
   one host, but never got far enough before hitting risk 2 above.

## Change log

- 2026-09-16: Initial decision, recorded after implementation landed
  end-to-end (`pkg/meshnet`, `MeshPeerer` on `hyperv`/`docker`, `agentproto`/
  `agentsdk` wiring, sandbox-triggered full-mesh peering). Real-hardware
  validation attempted on wks01 but blocked by the two-agents-one-host
  topology gap recorded above before reaching mesh peering itself.
- 2026-09-18: **Docker `MeshPeerer` exercised end to end for real** — not on
  a second Hyper-V host (still unavailable), but with two genuinely
  independent Docker daemons (a Linux/WSL host and a nested `docker:dind`
  container, each with its own network namespace) standing in for two
  hosts, sidestepping the Hyper-V two-agents-one-host topology gap above
  entirely since Docker's per-daemon resource listing has no shared-state
  ambiguity to begin with. Three real, previously-undiscovered bugs were
  found and fixed this way — unit tests use fakes for the interface/route
  layer and had no way to catch any of them:
  1. Docker's `SegmentRef` (a 64-character network ID) used directly as a
     Linux TUN device name, which the kernel rejects (`IFNAMSIZ` allows at
     most 15 usable characters) — every cross-host attempt failed
     immediately with "invalid argument". Fixed: `pkg/meshnet.
     SafeInterfaceName` derives a short, deterministic name from a hash
     when the id is too long, unchanged otherwise.
  2. `wireguard-go`'s `device.Device.Up` never touches the interface's
     kernel link state (only wg-quick's own separate `ip link set up` step
     normally does that) — the TUN device stayed administratively down, so
     every route through it failed with "Network is down". Fixed: bring
     the interface up via `github.com/vishvananda/netlink` on Linux
     (`pkg/meshnet/ifup_linux.go`); a documented no-op on other platforms.
  3. `AddMeshPeer` configured WireGuard's own crypto-routing (`allowed_ips`)
     but never added a kernel route for the peer's subnet through the local
     interface, so the kernel never handed cross-host traffic to WireGuard
     at all — sandboxes reached `ready` (the control-plane handshake
     succeeded) while containers genuinely could not reach each other.
     Fixed: `AddMeshPeer` now adds that route too.

  With all three fixed, a real cross-host sandbox reaches `ready` and the
  route tables/interface state on both sides confirm the tunnel is live and
  correctly configured. **A full packet-level ping proof was inconclusive**,
  not from a further product bug but from this test session's own harness
  cruft: the same two Docker daemons reused across ~18 iterations had
  accumulated leftover networks whose default-allocated subnets happened to
  collide across hosts, and re-chasing that within remaining session budget
  wasn't pursued. This is itself worth flagging as a real open question,
  independent of the three bugs above: **two Docker hosts with overlapping
  default address pools is a plausible production scenario** (most fresh
  Docker installs allocate from the same starting `172.17.0.0/16` upward),
  and nothing here currently detects or resolves that collision — a route
  added on top of a pre-existing local route for the same CIDR (this ADR's
  own `MeshIdentity`/`AddMeshPeer` "File exists" idempotency tolerance) is
  not necessarily *the peer's* route, it may just as easily be a different,
  unrelated local network that happens to share the same subnet.

  Also confirmed as a real, separate gap while implementing the fix above
  (not itself fixed): `RemoveMeshPeer` cannot remove the specific route
  `AddMeshPeer` added, since the `MeshPeerer` interface only passes the
  departing peer's public key, not its CIDR. Harmless for a two-host mesh
  (`DestroySegment` closes the whole interface, taking every route on it
  with it) but would leak a stale route in a mesh spanning three or more
  hosts that removes exactly one peer while keeping the interface up for
  the others. Revisit if/when a mesh larger than two hosts is exercised.

  Hyper-V's `MeshPeerer` implementation was not touched by any of this —
  its `SafeInterfaceName` call was deliberately left as a no-op (its
  switch-name-derived refs are short today) and it has its own separate
  interface bring-up path this session did not exercise or verify.
  **Hyper-V cross-host mesh peering remains entirely unverified.**
