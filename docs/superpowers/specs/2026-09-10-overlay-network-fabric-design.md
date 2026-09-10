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

This design closes both gaps, plus adds egress policy (an outbound
allow-list instead of open NAT) and just-in-time (JIT) native-protocol
access to one resource (SSH/RDP) without permanently exposing it.

### Non-goals for this design

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

### 1. Isolation is driver-native, not a deployed appliance — in scope

Boxy already has a place for "know how to manage resources on this
provider": the driver. Hyper-V's agent runs on the host with full host
access already; creating a `Private`/`Internal` vSwitch and a NAT object
(`New-VMSwitch`, `New-NetNat`) is the same *kind* of operation as creating
a VM, one level up. Docker's own `docker network create` already gives an
isolated bridge network per call, with no cross-network reachability by
default.

**New optional driver capability, `providersdk.NetworkIsolator`**
(detected by type assertion, same pattern as every other optional
capability in this package):

```go
type NetworkIsolator interface {
    CreateSegment(ctx context.Context, sandboxID model.SandboxID) (SegmentRef, error)
    DestroySegment(ctx context.Context, ref SegmentRef) error
    AllowEgress(ctx context.Context, ref SegmentRef, rule EgressRule) error
    RevokeEgress(ctx context.Context, ref SegmentRef, rule EgressRule) error
}
```

- Hyper-V implements it: a dedicated `Private` (single host) or `Internal`
  (needs NAT) vSwitch per sandbox, `New-NetNat` for egress, Windows
  Firewall rules scoped to that NAT for the allow-list.
- Docker implements it: a dedicated bridge network per sandbox, iptables
  rules scoped to that network's NAT for the allow-list.
- devfactory does **not** implement it, initially — same reasoning as its
  documented `GuestPersonalizer` non-goal (one real consumer's mechanics,
  no second real provider yet to validate a simulated contract against).
  Control-plane-level tests (fulfiller wiring, JIT reconciliation) use a
  purpose-built test fake that implements the interface directly, the same
  way `PoolResourceMaintenance` fakes are already used in server tests —
  not devfactory.

`SegmentRef` is an opaque, provider-defined identifier (switch/network
name) a resource's own `Create` call is given to attach to, replacing
whatever shared switch/network config it used before.

### 2. Cross-host connectivity is a new provider-neutral `pkg/` primitive — in scope

**`pkg/meshnet`** owns the agent-side `wireguard-go`
(`golang.zx2c4.com/wireguard`) device lifecycle: one WireGuard interface
**per sandbox that spans hosts**, never a shared interface across
sandboxes — this preserves the "one sandbox's bug can't leak into
another's" property at the OS-object level, the same way the per-sandbox
vSwitch does. `meshnet` is what a `NetworkIsolator` implementation plugs
its segment's routing into when a sandbox needs a cross-host hop; drivers
never touch WireGuard directly.

**Key handling:** each agent generates its own WireGuard keypair locally,
per segment, on demand. The private key **never leaves the agent
process** — not sent to `boxy serve`, not persisted anywhere else. Only
the public key is reported to `boxy serve` as part of segment creation.

**Peer introduction rides the existing ADR-0005 transport.** `boxy serve`
already holds an authenticated gRPC connection to every agent (mTLS,
per-agent identity). When a sandbox's resources land on Host-1 and
Host-2, `boxy serve` collects each host's public key + reachable endpoint
(from segment creation) and relays Host-2's info to Host-1's agent and the
mirror to Host-2's, over that existing channel — no new transport, no
extra registration step. Each agent's `meshnet` component then peers
directly with the other, and the actual encrypted tunnel is host-to-host,
direct UDP — `boxy serve` is never in that data path, only in the
introduction. This is architecturally the same role Headscale plays for
Tailscale (coordinator, not relay); Boxy already has the coordinator, so
nothing analogous to Headscale itself needs to be stood up separately.

A single-host sandbox never touches `meshnet` at all — WireGuard peer
count scales with concurrent *multi-host* sandboxes, not total sandbox
count.

### 3. Egress is allow-list, deny-by-default — in scope

A sandbox spec may declare an egress allow-list (hostnames/CIDRs). At
`CreateSegment` time (and via `AllowEgress`/`RevokeEgress` for later
changes), the driver translates it into scoped NAT/firewall rules on that
segment. No rule, no route out — a missing allow-list entry fails closed,
not open.

### 4. JIT native-protocol access rides the existing control-plane connections, not WireGuard — in scope

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
  `resource_id` + `port`) creates a `model.JITSession`
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

### 5. `AccessBroker` extension point — interface defined now, no implementation

For operators who already run Teleport or Boundary, Boxy's own JIT
session/relay (Decision 4) is redundant machinery duplicating what those
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

Unconfigured (the default): Decision 4's relay is what `boxy connect`
uses. Configured: sandbox fulfillment/deletion calls
`Register`/`DeregisterSandbox` instead, and `boxy connect` becomes a thin
wrapper printing whatever connection instructions the broker's own
tooling expects. The concrete Teleport/Boundary implementations, and
whether this needs to be pluggable at the config level or compiled in,
are explicitly deferred to whichever future issue actually builds one —
this section exists so that future work has a defined seam to build
against instead of retrofitting one later.

## Error Handling

- `CreateSegment` runs, and must succeed, before any resource `Create`
  call for that sandbox — a failure fails the sandbox cleanly with no
  segment or resource left behind, matching the existing package-input
  validation rule ("Check required package capabilities before provider
  allocation begins; a validation error should not leave an allocated
  resource behind").
- For a multi-host sandbox, per-host `CreateSegment` calls happen first
  (each independently valid even for a single host); peering is only
  attempted once resources are confirmed to span hosts. A peering failure
  tears down both segments rather than leaving one-sided connectivity.
- Segments and `JITSession`s both get the transient-state treatment
  ADR-0006 already established for resource destroy: mid-teardown is
  observable (not just vanishing), and both get an orphan sweep on
  crash/reconnect, following the same reasoning ADR-0006 already documents
  for why an earlier narrower split left a real recovery gap.
- Egress `AllowEgress`/`RevokeEgress` failures do not roll back segment
  creation — a segment with no successfully-applied egress rules is still
  a valid, fully-isolated (deny-all-egress) segment; the sandbox request
  itself surfaces the rule-application failure rather than failing silently
  or over-permitting.

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
