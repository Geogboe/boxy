# ADR-0021: Per-sandbox network isolation as an optional driver capability

## Status

Accepted (2026-09-10).

## Context

Two resources in two different sandboxes, provisioned from the same pool onto
the same provider host, currently share that host's default network — the
Docker daemon's default bridge, or whatever Hyper-V switch the pool's
`config.switch` names. Nothing stops one sandbox's container from reaching
another's, which is the wrong default for an environment whose whole purpose
is to hand disposable, untrusted workloads to an agent (#224).

Isolation has to be provider-specific: Docker has first-class per-network
primitives, Hyper-V has switches plus host NAT, and a future provider may
have neither. It also cannot happen at `Driver.Create` time. Boxy provisions
resources into a pool's ready inventory ahead of any sandbox (preheat), and a
sandbox later claims an already-created resource in a separate allocation
step — at create time there is no sandbox to isolate for yet.

## Decision

### A new optional capability, detected by type assertion

`providersdk.NetworkIsolator` (`pkg/providersdk/network_isolation.go`) is an
optional driver capability, discovered by type-asserting a `Driver` value,
exactly like `AvailabilityReporter`, `ResourceLister`, `GuestPersonalizer`,
`RelativePathResolver`, `LockedProvisioner`, and `NetworkRangeReporter`
already are. It is not added to `Driver` itself: a provider with no network
model of its own must stay a valid driver.

Three methods, split along the preheat/allocation seam described above:

- `CreateSegment(ctx, sandboxID) (SegmentRef, error)` — once per sandbox, on
  its first resource claim.
- `AttachToSegment(ctx, providerResourceID, ref) error` — once per claimed
  resource, at allocation time. This runs on the sandbox-creation hot path,
  so it must be a single lightweight reconnect, not a provisioning-shaped
  operation.
- `DestroySegment(ctx, ref) error` — teardown, idempotent for an
  already-gone segment, matching `Driver.Delete`'s contract.

`SegmentRef` is an opaque provider-defined string (a switch name for Hyper-V,
a network ID for Docker). Callers never interpret it; they hold what
`CreateSegment` returned and pass it back unchanged.

### Idempotency is part of the contract, not a per-driver accident

All three methods are documented idempotent, because every one of them can be
retried by a caller that crashed or timed out partway:

- `CreateSegment` is idempotent **per `sandboxID`** — a repeat returns the
  same `SegmentRef`. Without this, a retry either fails on a duplicate name
  or strands a second segment that nothing tears down, since only the ref the
  caller ends up holding is ever passed to `DestroySegment`.
- `AttachToSegment` is idempotent on an already-attached resource, so a retry
  after a partial attach converges rather than erroring.
- `DestroySegment` is idempotent on an already-gone segment.

The two implementations reached idempotency independently and divergently
during implementation; writing it into the interface doc is what makes it a
contract a third driver has to honor rather than a coincidence.

### `sandboxID` is assumed name-safe at the boundary

`CreateSegment`'s doc states that `sandboxID` is expected to already be safe
inside a provider-specific resource name — no spaces, DNS-label-safe
characters — and that implementations need not sanitize further. Boxy's own
sandbox IDs (`sbx_` plus 16 hex characters) satisfy this by construction, so
the alternative would be every driver inventing its own escaping for input
that is never actually unsafe, with no shared definition of "safe" to test
against. Hyper-V's `switchNameForSandbox` still strips spaces as a cheap
belt-and-braces measure and Docker appends the ID verbatim; under this
documented assumption both are correct.

### Hyper-V: Internal vSwitch + per-sandbox NAT over a ledger-tracked CIDR

`hyperv.Driver` carves each sandbox a `/29` block out of a fixed
`10.250.0.0/16` base (`segmentBaseCIDR`), chosen from RFC 1918 space well
clear of the `10.0.x.x`/`10.1.x.x` end of `10.0.0.0/8` that operator LANs
usually occupy. A `/29` is 8 addresses — network, gateway, up to 5 usable
hosts, broadcast — enough for a small lab. `segmentPrefixLen` is the single
source of truth: the persisted CIDR string, the `New-NetIPAddress
-PrefixLength` argument, and the block arithmetic all derive from it.

Assignments live in a disk-backed segment ledger
(`network-segments.json` under `Config.DataDir`, built on `pkg/diskjson` and
mirroring the existing `ledger.go` IP-range ledger, including its
`sync.Once`-shared-instance rule so two concurrent allocations cannot each
lock their own mutex over the same file). The ledger is what makes
`CreateSegment` idempotent — a repeat finds the sandbox's existing entry —
and it keeps a `FreedIndexes` free list so released blocks are reused before
the high-water mark advances.

`CreateSegment` then runs one PowerShell script that creates an **Internal**
vSwitch (not Private: Private would also cut off host-provided NAT, i.e. all
internet access, which is the wrong default until egress policy exists to
restrict it deliberately), assigns the block's gateway address to the
switch's host-side `vEthernet (<switch name>)` adapter, and adds a
`New-NetNat` for the block. Every step is guarded by its own `Get-` existence
check, so a retry resumes where a partial attempt left off. A failed script
deliberately does **not** release the ledger entry: the retry must resolve
the *same* CIDR and switch name, or those existence checks would paper over a
mismatch instead of resuming. `AttachToSegment` is a live
`Connect-VMNetworkAdapter` — the same no-restart reconnect `Driver.Create`
already uses.

### Docker: one bridge network per sandbox, connect before disconnect

`docker.Driver` creates a bridge network named `boxy-sb-<sandboxID>` with the
driver's managed label, and sets no explicit IPAM — the daemon auto-assigns a
non-overlapping subnet from its own address pools, so unlike Hyper-V there is
no collision-avoidance ledger to keep. Idempotency comes from a
`NetworkInspect` lookup on that deterministic name before creating; a
`NetworkCreate` that fails anyway re-resolves once more, so the loser of a
concurrent-create race converges on the winner's network instead of
propagating a duplicate-name error.

`AttachToSegment` connects the container to the segment and **then**
disconnects it from every other network it was on. The disconnect half is
what actually delivers the isolation — `Driver.Create` attaches new
containers to the daemon's default bridge, so connecting alone would leave
the container multi-homed on both the shared bridge and its private segment,
reachable from every other container on that host. The ordering is
deliberate: disconnect-first would leave the container with no network at all
if the connect then failed. Disconnects are sorted for a deterministic call
sequence, and a not-found on disconnect is tolerated as the desired end
state.

### `devfactory` deliberately does not implement it

Same reasoning already recorded for `GuestPersonalizer` (#181) and
`NetworkRangeReporter` (#223) in AGENTS.md: this capability has exactly one
real implementation per platform and no second provider to validate a
*simulated* contract against, so simulating it would mean inventing
Docker- or Hyper-V-specific network semantics wearing a generic-looking
interface. `devfactory` remains the control-plane simulator; capability
fixtures it cannot faithfully model are a written non-goal, not a backlog
item. Revisit only for a concrete testing need with its own scoping writeup.

## Alternatives considered

- **Add the methods to `providersdk.Driver`.** Rejected: it would force every
  provider, including ones with no network model, to implement or stub three
  methods, and there is no meaningful no-op for "isolate this".
- **Isolate at `Driver.Create` time.** Not possible — preheated resources
  exist before any sandbox claims them.
- **Hyper-V Private switches.** Rejected: no host NAT means no egress at all,
  a harsher default than the problem warrants, and unreversible without an
  egress-policy feature that does not exist yet.
- **A Docker-side IPAM ledger mirroring Hyper-V's.** Unnecessary: the Docker
  daemon already owns non-overlapping subnet assignment.

## Consequences

- A third driver adding this capability inherits a real contract
  (idempotency, the name-safety assumption, the fast-attach constraint)
  instead of copying whichever existing implementation it happened to read.
- Hyper-V gains a second piece of persistent per-host state. Losing
  `network-segments.json` means losing track of which `/29` blocks are in
  use, the same class of hazard ADR-0012 records for the IP-range ledger —
  which is why `Config.DataDir` defaults to `.boxy-agent/hyperv` rather than
  an ephemeral location.
- Both implementations are unit-tested against fakes only. The Docker driver
  is exercised through a mocked `dockerClient`; the Hyper-V driver through a
  fake PowerShell executor. Neither path has been run against live
  infrastructure from the development host (see AGENTS.md's "This development
  host cannot run Hyper-V VMs" note), which leaves the two open risks below.
- Nothing consumes the capability yet. Plan 1a is driver-side only; wiring
  sandbox creation/allocation/teardown to call it is Plan 1b, which is also
  where the two risks below have to be settled.

## Open risks for Plan 1b

Both are unverifiable from the development host and neither is fixed here —
they are recorded so the consumer-side work starts by resolving them, not by
rediscovering them:

1. **`New-NetNat` may be effectively one-instance-per-host.** Windows' NAT
   implementation has historically behaved as though only a single NAT
   instance can exist at a time, or at least that overlapping/multiple
   instances are unsupported. If that holds, the Hyper-V design is
   non-functional past the first sandbox on a given host: the second
   `New-NetNat` fails and `CreateSegment` errors. Verifying this on a real
   Hyper-V host is a prerequisite for Plan 1b; a fallback would likely be one
   shared NAT over the whole `10.250.0.0/16` base with per-sandbox isolation
   resting on the separate switches alone.
2. **`New-NetIPAddress` immediately after `New-VMSwitch` assumes the host
   adapter has already materialized.** `CreateSegment` addresses
   `vEthernet (<switch name>)` in the same script that just created the
   switch. The host-side vNIC is expected to appear synchronously with
   `New-VMSwitch`, but that is plausible rather than verified; if it is
   actually asynchronous, the script will intermittently fail on a
   freshly-created switch and need a bounded wait-for-adapter retry.

## Change log

- 2026-09-10: Initial decision, recorded with Plan 1a (driver-side
  implementation of the capability for Hyper-V and Docker). Part of #224.
