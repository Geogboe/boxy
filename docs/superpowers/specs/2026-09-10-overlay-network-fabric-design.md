# Design: Boxy-Owned Overlay Network Fabric (#224)

## Context

#224 is a deferred "research" issue: WireGuard-go, gateway-VM-per-host, not
per-guest. Its own text lists open questions it deliberately left
unresolved (mesh topology, how key/route distribution rides ADR-0005's
trust, whether a gateway-VM-per-host is even the right shape). This
document resolves those questions and supersedes #224's specific
gateway-VM proposal with a leaner, driver-native design arrived at through
back-and-forth review — the gateway-VM idea was a reasonable starting
point but dissolves once the problem is decomposed correctly (see
"Rejected: gateway-VM-per-host" below).

### The actual gap, stated precisely

Today, when a sandbox's resources land on a Hyper-V (or Docker) host, they
attach to whatever switch/network the pool's provider config already
points at. That switch/network is typically shared across every pool and
every sandbox on that host. Concretely: **two unrelated sandboxes' VMs on
the same host can currently reach each other** — there is no isolation
boundary between tenants sharing a host today. Separately, a sandbox whose
resources are split across two hosts (because that's where capacity was)
has no way for those resources to reach each other at all — each host's
switch is a separate, unconnected L2 segment.

This design closes both gaps and adds just-in-time (JIT) native-protocol
access to one resource (SSH/RDP) without permanently exposing it. Egress
policy (restricting *what* a sandbox can reach outbound) turned out to be
its own real design problem once examined — see "Egress policy" under
Non-goals — and is deliberately not part of this design.

### Non-goals for this design

- **Egress policy (restricting what a sandbox can reach outbound).**
  Examined during design review and found to be its own real design
  problem, not a bullet point inside this one: does a rule apply
  per-sandbox, per-pool, or globally? Who's allowed to define one
  (operator only, or can a sandbox requester propose rules subject to
  approval)? Does "allow github.com" mean HTTPS only, or any protocol on
  that host? Does DNS resolution itself need to be controlled (open DNS
  lets a sandbox exfiltrate data through query names alone, regardless of
  what the firewall blocks afterward)? There's also a real mechanism
  question underneath all of that: a firewall/NAT rule can only match IP
  addresses, not hostnames (hostnames never appear in a packet — only in
  the DNS lookup that preceded it), so a hostname-shaped allow-list
  needs either IP/CIDR-only rules (blunt, IPs can rotate) or an actual
  hostname-aware proxy in the path (accurate, but new infrastructure to
  build and run). None of this is resolved here — it's a separate future
  design session. **Interim default:** each sandbox's new private network
  (Decision 1) gets unrestricted outbound NAT, matching the effectively
  open internet access sandboxes already have today — this design changes
  *isolation between sandboxes*, not what any one sandbox can already
  reach outbound. Nothing here should be read as "sandboxes lose internet
  access until egress policy ships."
- **NAT traversal / relay fallback for hosts that cannot reach each other
  directly over UDP.** This assumes hosts running Boxy agents are
  operator-controlled infrastructure that can already reach each other
  (the same requirement most on-prem clustering/storage already has) — not
  hosts split across a network with a locked-down posture that blocks
  arbitrary outbound UDP. A relay component (conceptually similar to
  Tailscale's DERP) is the real fix if this ever becomes a genuine need;
  it is deliberately not designed here. Revisit only with a concrete
  deployment that hits this, per this project's own established pattern
  for scoping non-goals (see AGENTS.md's `GuestPersonalizer` precedent).
- **A concrete Teleport/Boundary integration.** This design defines the
  extension point (`AccessBroker`, see below) where such an integration
  would plug in, replacing Boxy's own JIT relay for operators who already
  run one of those. It does not implement either integration.
- **Docker/Linux gateway appliance details beyond what `NetworkIsolator`
  already covers.** The interface is provider-neutral; a Linux/Docker
  implementation follows the same shape as Hyper-V's, using Docker's own
  network primitives instead of vSwitches. Full worked-out Docker
  mechanics are left to that implementation's own PR, same as the
  Hyper-V-only detail already accepted for `NetworkRangeReporter` (ADR-0013).
- **Job-scheduler (#124) and control-plane/agent framework extraction
  (#211) overlap.** Both are separate, larger design efforts. This design
  does not depend on either landing first, and nothing here blocks them.

## Rejected: gateway-VM-per-host (#224's original proposal)

#224 proposed a single Boxy-owned gateway VM per host, fronting/replacing
the host's shared NAT switch, transparent to tenant guests. Reconsidered
during design review because it doesn't actually solve the isolation gap
above: with one shared gateway per host, per-sandbox isolation would have
to be implemented as *software state inside that one shared VM*
(namespaces, VLAN tags, or WireGuard `AllowedIPs` subsets per sandbox) —
a bug or compromise in that shared gateway threatens every sandbox on the
host at once. It also adds a full VM (and a "keep exactly one alive per
host" reconciliation loop) to do work a driver can already do natively on
the host itself.

## Decisions

### 1. Isolation is driver-native, automatic for every sandbox, not a deployed appliance — in scope

Boxy already has a place for "know how to manage resources on this
provider": the driver. Hyper-V's agent runs on the host with full host
access already; creating an `Internal` vSwitch and a NAT object
(`New-VMSwitch`, `New-NetNat`) is the same *kind* of operation as creating
a VM, one level up. Docker's own `docker network create` already gives an
isolated bridge network per call, with no cross-network reachability by
default.

This is **automatic for every sandbox, not opt-in.** Today's leaky shared
switch is a real bug (see "the actual gap" above), not a missing feature
someone has to ask to fix — every sandbox gets its own segment
unconditionally. The cost (one extra switch/network object created and
torn down per sandbox) is worth paying universally rather than leaving the
default leaky for anyone who doesn't know to ask.

**Segment creation cannot happen at resource-`Create` time.** Boxy
provisions resources into a pool's ready inventory ahead of any sandbox
(preheat) — `Create` runs with no sandbox in the picture yet, and a
sandbox later *claims* an already-created, already-network-attached
resource via a separate `Allocate` call. A per-sandbox segment therefore
has to be created (once, on the sandbox's first claim) and each claimed
resource *moved onto it* at allocation time, not creation time — the same
timing this codebase already uses for network identity: `GuestPersonalizer`
only applies IP configuration when `GuestPersonalizationOptions.ApplyNetwork`
is true, which is set only at allocation, never at preheat, so an
unclaimed preheated VM is never needlessly reconfigured. `NetworkIsolator`
follows the same rule, via a third method:

**New optional driver capability, `providersdk.NetworkIsolator`**
(detected by type assertion, same pattern as every other optional
capability in this package):

```go
type NetworkIsolator interface {
    CreateSegment(ctx context.Context, sandboxID model.SandboxID) (SegmentRef, error)
    AttachToSegment(ctx context.Context, providerResourceID string, ref SegmentRef) error
    DestroySegment(ctx context.Context, ref SegmentRef) error
}
```

`AttachToSegment` moves an already-created resource off whatever
pool-level switch/network it was created on and onto the sandbox's
segment — for Hyper-V, `Connect-VMNetworkAdapter -SwitchName` (works on a
running VM, no restart, and Hyper-V supports this as a live reconnect);
for Docker, disconnect the container from its default network and connect
it to the sandbox's dedicated one. **This must be fast — sandboxes need to
be ready near-instantly, so this is a single lightweight reconnect
operation, not anything that blocks on a slow provisioning-style step.**

(Egress control — restricting what a segment can reach outbound — is
deliberately not part of this interface; see "Egress policy" under
Non-goals. A future design would extend this capability, not replace it.)

- Hyper-V implements it: a dedicated `Internal` vSwitch per sandbox (host
  and VMs can talk to it, but it's not bridged to any other sandbox's
  switch) plus `New-NetNat` bound to that switch's subnet, unrestricted —
  see the egress non-goal above for why this is open, not filtered, for
  now. (An `Internal` switch, not `Private`: `Private` would also cut off
  the host-provided NAT, i.e. all internet access — the wrong default
  until egress policy exists to restrict it deliberately instead.)
- Docker implements it: a dedicated bridge network per sandbox — Docker's
  own default NAT/egress behavior is unchanged, only the per-sandbox
  network boundary is new.
- devfactory does **not** implement it, initially — same reasoning as its
  documented `GuestPersonalizer` non-goal (one real consumer's mechanics,
  no second real provider yet to validate a simulated contract against).
  Control-plane-level tests (fulfiller wiring, JIT reconciliation) use a
  purpose-built test fake that implements the interface directly, the same
  way `PoolResourceMaintenance` fakes are already used in server tests —
  not devfactory.

`SegmentRef` is an opaque, provider-defined identifier (switch/network
name) `AttachToSegment` uses to know which segment to move a resource
onto, and that new resources allocated later into the same sandbox reuse
(one `CreateSegment` call per sandbox, many `AttachToSegment` calls — one
per resource claimed into it).

### 2. Cross-host connectivity is a new provider-neutral `pkg/` primitive — in scope

**`pkg/meshnet`** owns the agent-side `wireguard-go`
(`golang.zx2c4.com/wireguard`) device lifecycle. Each host a given
sandbox actually touches gets its own WireGuard interface **for that
sandbox specifically** — a sandbox spanning 3 hosts means 3 interfaces
(one per host), never one interface shared across different sandboxes on
the same host. This preserves the "one sandbox's bug can't leak into
another's" property at the OS-object level, the same way the per-sandbox
vSwitch does. `meshnet` is what a `NetworkIsolator` implementation plugs
its segment's routing into when a sandbox needs a cross-host hop; drivers
never touch WireGuard directly.

**Key handling:** each agent generates its own WireGuard keypair locally,
per segment, on demand. The private key **never leaves the agent
process** — not sent to `boxy serve`, not persisted anywhere else. Only
the public key is reported to `boxy serve` as part of segment creation.

**The reachable endpoint address is operator-configured, not
auto-detected.** A host can be multi-homed (several NICs/IPs), and
guessing which one is reachable by other Boxy hosts is exactly the kind
of inference this project avoids elsewhere (e.g. `hyperv.Config.DataDir`
requires an explicit path rather than inferring one). Each agent's own
config declares the address it should be dialed at for mesh traffic — the
same explicit-over-inferred posture ADR-0012/ADR-0013 already establish
for this driver.

**Peer introduction rides the existing ADR-0005 transport.** `boxy serve`
already holds an authenticated gRPC connection to every agent (mTLS,
per-agent identity). When a sandbox's resources land on Host-1 and
Host-2, `boxy serve` collects each host's public key (from segment
creation) and its configured mesh endpoint, and relays Host-2's info to
Host-1's agent and the mirror to Host-2's, over that existing channel —
no new transport, no extra registration step. Each agent's `meshnet`
component then peers directly with the other, and the actual encrypted
tunnel is host-to-host, direct UDP — `boxy serve` is never in that data
path, only in the introduction. This is architecturally the same role
Headscale plays for Tailscale (coordinator, not relay); Boxy already has
the coordinator, so nothing analogous to Headscale itself needs to be
stood up separately.

A single-host sandbox never touches `meshnet` at all — WireGuard peer
count scales with concurrent *multi-host* sandboxes, not total sandbox
count.

### 3. JIT native-protocol access rides the existing control-plane connections, not WireGuard — in scope

Initially designed as an embedded WireGuard client in the CLI (so an
operator's laptop could dial a resource directly, mirroring the
agent-to-agent mesh). Rejected during design review: this is a genuinely
different problem from cross-host mesh traffic. An interactive human
session (one person, one SSH/RDP stream) has the same traffic profile
`kubectl port-forward` and Teleport/Boundary sessions have, and all three
solve it the same way — relaying through the control-plane connection
that already exists and is already known-firewall-friendly, rather than
opening a new path. (`kubectl port-forward` specifically: the client talks
to the apiserver over the normal cluster HTTPS connection, which proxies
the stream to the kubelet over the cluster's already-established
control-plane connection; the kubelet is the one that actually reaches
into the pod's network namespace. No separate network path is ever
opened.)

Boxy already has the equivalent chain: `boxy` CLI → HTTPS API → `boxy
serve` → gRPC/mTLS → the target agent (ADR-0005). JIT access reuses it:

- `POST /api/v1/sandboxes/{id}/connect` (new endpoint; request names
  `resource_id` + `port`), gated the same way exec already is
  (`APIKeyRoleUser`/`APIKeyRoleAdmin`, restricted to the caller's own
  sandboxes — same authorization shape ADR-0007/ADR-0008 already
  established, not a new access model), creates a `model.JITSession`
  (`SandboxID`, `ResourceID`, `Port`, `Requester`, `ExpiresAt`) — persisted
  the same way `model.Sandbox` already is — and upgrades the HTTP
  connection to a bidirectional byte stream, following the same streaming
  contract ADR-0008 already established for exec.
- `boxy serve` relays that stream over the target agent's existing gRPC
  connection; the agent dials `resource:port` locally (same host, same
  segment) and pipes bytes back through the same chain.
- `boxy connect <sandbox> --resource <id> --port <n>` (new CLI command)
  exposes a local listener; the operator points a native client (`ssh`,
  `mstsc`) at it.
- **No client-side WireGuard, no new port, no UDP.** Nothing is installed;
  nothing persists after the command exits.

**Timer ownership is the control plane's, not the agent's** — the same
correction this project's own architecture already makes for sandbox
auto-destroy. A `JITSession`'s `ExpiresAt` (idle timeout, refreshed on
activity, capped by a max lifetime — same shape as sandbox exec's timeout
handling) is tracked and reconciled by a new ticked loop in `boxy serve`,
mirroring `internal/sandbox/deleter.go`. On expiry, `boxy serve` issues
exactly one command to the agent: close this dial. The agent never runs
its own timer or makes an autonomous decision — it only executes
create/close commands, same as every other driver operation. If the
agent was disconnected when expiry fired, the existing reconnect
orphan-sweep pattern (`internal/pool/manager.go`'s sweep for resources
stuck mid-transition after a crash) re-evaluates every `JITSession`
against its `ExpiresAt` and re-issues `close` for anything already past
due — no new recovery mechanism, the existing one generalizes.

### 4. `AccessBroker` extension point — interface defined now, no implementation

For operators who already run Teleport or Boundary, Boxy's own JIT
session/relay (Decision 3) is redundant machinery duplicating what those
products already do better (session recording, audit, RBAC, a transport
already proven to cross hostile networks). Rather than build a concrete
integration now, this design reserves the seam:

```go
// AccessBroker, if configured, is asked to register/deregister a
// sandbox's resources as it comes up/down, instead of Boxy's own
// JITSession relay handling human access to them.
type AccessBroker interface {
    RegisterSandbox(ctx context.Context, sandbox model.Sandbox) error
    DeregisterSandbox(ctx context.Context, sandboxID model.SandboxID) error
}
```

Unconfigured (the default): Decision 3's relay is what `boxy connect`
uses. Configured: sandbox fulfillment/deletion calls
`Register`/`DeregisterSandbox` instead, and `boxy connect` becomes a thin
wrapper printing whatever connection instructions the broker's own
tooling expects. The concrete Teleport/Boundary implementations, and
whether this needs to be pluggable at the config level or compiled in,
are explicitly deferred to whichever future issue actually builds one —
this section exists so that future work has a defined seam to build
against instead of retrofitting one later.

## Error Handling

- `CreateSegment` runs, and must succeed, on a sandbox's first resource
  claim, before that resource's `AttachToSegment` call — a failure fails
  the claim cleanly rather than leaving a resource allocated but
  unattached. `AttachToSegment` failing for a *later* resource claimed
  into an already-segmented sandbox fails only that claim, not the whole
  sandbox or its already-attached resources — matching the existing
  package-input validation rule ("Check required package capabilities
  before provider allocation begins; a validation error should not leave
  an allocated resource behind").
- For a multi-host sandbox, per-host `CreateSegment` calls happen first
  (each independently valid even for a single host); peering is only
  attempted once resources are confirmed to span hosts. A peering failure
  tears down both segments rather than leaving one-sided connectivity.
- Segments and `JITSession`s both get the transient-state treatment
  ADR-0006 already established for resource destroy: mid-teardown is
  observable (not just vanishing), and both get an orphan sweep on
  crash/reconnect, following the same reasoning ADR-0006 already documents
  for why an earlier narrower split left a real recovery gap.
- A `JITSession`'s create call fails closed: if the agent cannot dial
  `resource:port` (wrong port, resource not listening, resource gone), the
  session is never persisted and the CLI surfaces the dial failure
  directly rather than handing back a listener that silently does nothing.

## Testing

- `pkg/meshnet`: pure Go, two loopback `wireguard-go` instances exercising
  real peer handshake/data flow — no host dependency, no live Hyper-V
  needed.
- Hyper-V `NetworkIsolator`: fake PowerShell executor, following the
  existing pattern the rest of that driver already uses for
  host-management calls this dev environment cannot run live.
- Docker `NetworkIsolator`: fake/injected Docker client, following
  whatever pattern the existing Docker driver tests already use.
- Fulfiller wiring, cross-host peer introduction, and JIT session
  reconciliation: a purpose-built test fake implementing
  `NetworkIsolator` directly (not devfactory — see Decision 1).
- JIT relay streaming: ADR-0008's existing fake-executor pattern for
  streaming exec, adapted for a plain byte-stream dial instead of a
  command execution.
- Live Hyper-V/Docker-host validation of the actual vSwitch/NAT/WireGuard
  interaction stays explicitly out of scope for this dev host, per this
  project's own standing "cannot run live Hyper-V VMs" constraint —
  called out here so implementation work doesn't claim otherwise.

## Open Follow-Ups (explicitly not this design's job to resolve)

- NAT traversal / relay fallback for hosts that can't reach each other
  directly over UDP (see Non-goals).
- Concrete `AccessBroker` implementation(s) for Teleport and/or Boundary.
- Docker/Linux `NetworkIsolator` implementation details (interface shape
  only is fixed here).
- Whether/how this intersects #211 (control-plane/agent framework
  extraction) or #124 (job-scheduler design) if either lands first.
