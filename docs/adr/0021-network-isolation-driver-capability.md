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
- The control plane does not consume the capability yet. Plan 1a is
  driver-side only; Plan 1b carried it up to `agentsdk.NetworkIsolatingAgent`
  so an agent can dispatch the three calls, but nothing asks it to. Wiring
  sandbox creation/allocation/teardown to actually call it is Plan 1c, which
  is also where the two risks below have to be settled.

## Open risks for Plan 1c

Both are unverifiable from the development host and neither is fixed here —
they are recorded so the consumer-side work starts by resolving them, not by
rediscovering them:

1. **`New-NetNat` may be effectively one-instance-per-host.** Windows' NAT
   implementation has historically behaved as though only a single NAT
   instance can exist at a time, or at least that overlapping/multiple
   instances are unsupported. If that holds, the Hyper-V design is
   non-functional past the first sandbox on a given host: the second
   `New-NetNat` fails and `CreateSegment` errors. Verifying this on a real
   Hyper-V host is a prerequisite for Plan 1c; a fallback would likely be one
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
- 2026-09-14: This ADR was written before Plan 1b landed, and at the time
  used "Plan 1b" for the consumer-side work. Plan 1b turned out to be agent
  wiring: it added `agentsdk.NetworkIsolatingAgent` — implemented by both
  `EmbeddedAgent` (type-asserting the local driver) and `RemoteAgent` (three
  new commands over the existing gRPC stream) — on top of the driver
  capability this ADR describes. That still creates no real segment from the
  control plane: nothing calls the agent capability either. The
  sandbox/allocation wiring that will is now Plan 1c
  (`docs/superpowers/plans/2026-09-10-overlay-network-isolation-sandbox-wiring-plan.md`),
  and both open Hyper-V risks above remain open, explicitly deferred to it.
  References to "Plan 1b" in Consequences and the open-risks section were
  updated accordingly. Part of #224.
- 2026-09-14 (Plan 1c, final-review fixes): the control plane now actually
  calls this capability, and the two Critical defects that whole-branch
  review found are fixed. Part of #224; the agreed design for both is in
  `docs/superpowers/specs/2026-09-14-plan-1c-review-fix-addendum.md`.

  **Capability advertisement (C1).** The daemon consults what an agent
  advertises it can isolate before ever calling `CreateSegment`, treating
  "not advertised" as skip-not-error. Previously the only real
  `providersdk.NetworkIsolator` check happened inside the agent and returned
  a hard error, so a devfactory-shaped agent — which deliberately does not
  implement this capability, see above — failed allocation outright. Landed
  as a parallel change across `pkg/agentsdk`, `internal/pool`, and
  `internal/sandbox`; see those files for the mechanics.

  **Segment-sourced Hyper-V guest addressing (C2).** `AttachToSegment` now
  assigns the guest's IPv4 address after the `Connect-VMNetworkAdapter` move,
  sourced from the segment's own ledger entry: the gateway takes the block's
  first usable address and the guest takes the next (`segmentGatewayOffset` /
  `segmentGuestOffset`). Without this the move was actively harmful — a
  segment's Internal vSwitch issues no DHCP, so a reconnected VM kept a
  stale, wrong-subnet address or fell back to APIPA, i.e. was isolated *and*
  unreachable. The address is derived from a constant offset rather than
  allocated, so `AttachToSegment` stays idempotent for free; `assignGuestIP`
  (unchanged, #235) does the in-guest work and is self-verifying. Linux
  guests are a hard error, matching every other boxy-managed in-guest
  addressing path — PowerShell Direct is Windows-only, and an unaddressed
  guest on an isolated switch is broken, not skippable.

  Consequently the Hyper-V driver's pool-declared `network` config
  (`static_ip`/`range` and friends) was removed entirely: with isolation
  automatic and universal, the segment is always the guest's real final
  network, so a pool-declared address could only ever be overwritten. See
  ADR-0012's and ADR-0013's 2026-09-14 entries.

  **This makes `AttachToSegment` heavier than the Decision section above
  frames it.** That text calls for "a single lightweight reconnect, not a
  provisioning-shaped operation" because the method runs on the
  sandbox-creation hot path. The Hyper-V implementation now additionally
  performs a `Get-VM` notes read, possibly a control-plane credential
  lookup, and opens a PSRP session — a real multi-second guest round trip
  (#361). That is an accepted trade, not an oversight: nothing else in the
  allocation sequence knows the segment's subnet, so there is no cheaper
  place to put it. It compounds #350 (per-sandbox timeout does not scale
  with resource count), which should be weighed with this cost in mind.

  **Guest credential at attach time — a gap the addendum's mechanics did not
  anticipate.** The addendum specified resolving the bootstrap credential via
  `resolveBootstrapCredential`, mirroring `personalizeGuestLocked`. Tracing
  the real allocation order showed that cannot work: `Allocate` calls
  `PersonalizeGuest`, which rotates the guest off whatever credential it
  authenticated with, and `internal/pool` then *deletes* the server-side
  per-resource credential, one-time-delivering the new value to the sandbox
  caller (ADR-0010). By the time `ensureNetworkSegment` runs, a bootstrap
  lookup returns the *pool* bootstrap, which the guest was rotated off at
  admission. The driver now retains, in memory only, the credential it
  rotated each guest onto (`rememberRotatedCredential`), recorded only after
  `verify_credential` proves the guest accepted it, and dropped when the
  resource is deleted. This does not widen ADR-0010's boundary — that value
  is already described as opaque and process-local, and it never reaches
  resource properties, VM notes, logs, the API, or agent config. Routing
  makes it sound: `ProviderRef.AgentID` sends `Allocate` and
  `AttachToSegment` for one resource to the same agent process.

  **Retention is scoped to allocation-time personalization**
  (`opts.ApplyNetwork`), which is the only phase an `AttachToSegment` can
  follow. An admission-time preheat rotation retains nothing: holding those
  passwords would mean the agent process kept the plaintext credential of
  every idle, unclaimed VM in every Hyper-V pool for that pool's entire
  preheat lifetime, to serve a call that may never come. This is #358's own
  principle — a preheated-but-unclaimed resource gets nothing it has no use
  for yet — applied to a secret rather than an address, and it is what makes
  `ApplyNetwork` load-bearing again now that it gates no in-guest addressing.

  **Known gap:** an agent restart between allocation and attach loses the
  retained value; the fallback to the (stale) bootstrap will usually fail.
  The failure is loud but not cheap — the authentication error propagates out
  of `AttachToSegment`, through `ensureNetworkSegment` as a hard error, and
  fails the allocation, after which the fulfiller rolls the sandbox back to
  `failed` and quarantines the resource. Still better than a silently
  mis-addressed guest, which is why the fallback stays, but the outcome is a
  failed sandbox plus a quarantined resource, not merely an unaddressed VM.
  This was a judgment call made during implementation and warrants a second
  look. Scoping retention to allocation time (see the paragraph above, added
  2026-09-15) does not widen this gap — the credential an attach needs is the
  one rotated during that same allocation, which a restart loses either way —
  but it does remove an accidental second chance, where a retained
  admission-time credential could previously have happened to still be
  current.

- 2026-09-15 (Plan 1c, combined re-review fixes): three defects the combined
  re-review of the fix round above found. Part of #224.

  **Segments are keyed per (agent, provider type), not per agent.**
  `ensureNetworkSegment`'s reuse branch matched only on `AgentID`, so a
  sandbox spanning two pools of different provider types on one agent handed
  the second provider the first's segment ref — a ref that provider cannot
  address, failing the whole allocation. That is the *default* daemon shape,
  not an edge case: `internal/cli/serve.go` builds one embedded agent over
  every configured driver, so every mixed-provider sandbox shares one agent
  ID. A segment is a provider-specific host object (a vSwitch, a Docker
  network), so one agent hosting two drivers legitimately owns two segments
  for the same sandbox. Reuse now matches `model.NetworkSegment.ProviderType`
  against the resource's own `Provider.Name` as well.

  **`DestroySegment` gained the advertisement check `CreateSegment` already
  had**, returning the same `ErrNetworkIsolationUnsupported` sentinel for an
  agent that is registered but no longer advertises isolation for the
  segment's provider type. Without it, an agent reconfigured or downgraded
  between allocation and deletion produced a hard error that blocked its
  sandbox's deletion permanently and — since `Reconcile` returns on the first
  cleanup error — stalled every later sandbox in the same tick, reproducing
  the failure mode the unregistered-agent sentinel was added to prevent.

  **Credential retention was scoped to allocation time**; see the retention
  paragraph in the 2026-09-14 entry above for the reasoning.

## Open risks — status after Plan 1c

Both risks recorded above were carried into Plan 1c to be settled there.
Neither could be settled empirically: this development host cannot run
Hyper-V VMs (see AGENTS.md), so everything below rests on fakes.

1. **`New-NetNat` one-instance-per-host — CLOSED 2026-09-22, disproven on
   real hardware.** Verified directly on wks01 (Windows 10 Pro): two
   concurrent `New-NetNat` instances with non-overlapping `/29` prefixes
   (`10.250.90.0/29` and `10.250.91.0/29`), each over its own Internal
   vSwitch with a host vNIC address — exactly the shape
   `hyperv.CreateSegment` produces per sandbox — were created successfully
   and coexisted. Windows did not refuse the second instance, so the
   per-sandbox NAT design works as written and no fallback is required.

   The fallback sketched above was also confirmed available should a
   future host behave differently: one shared NAT over `10.250.0.0/16`
   coexists fine with two separate per-sandbox switches addressed inside
   that range. Note this only establishes that the NAT objects can be
   created and coexist; guest-to-internet traffic through two concurrent
   NATs was not exercised, and the multi-sandbox path still hasn't been
   run end to end through the daemon on real Hyper-V hardware.
2. **`New-NetIPAddress` timing right after `New-VMSwitch` — still
   theoretical, and now better contained.** No timing failure was observed,
   because nothing here has been run against a live host; no bounded
   wait-for-adapter retry was added, since inventing one against a hazard
   that cannot be reproduced would be untestable code guarding a guess. Two
   things changed in its favor. The host-side sequence itself is *unchanged*
   from Plan 1a — `CreateSegment`'s script still addresses
   `vEthernet (<switch name>)` in the same script that created the switch —
   so this fix neither worsens nor improves that specific window. And the new
   guest-side step runs strictly later (after `CreateSegment` completes and
   after `Connect-VMNetworkAdapter`), where the analogous hazard is an
   adapter not yet visible *inside the guest*; there, `assignGuestIP`'s
   script already throws `no network adapter found in guest` rather than
   silently succeeding, so a materialization lag fails loudly and a retried
   `AttachToSegment` converges. If a real host does show intermittent
   `New-NetIPAddress` failures on a fresh switch, the fix belongs in
   `CreateSegment` and should be recorded here.

## Change log (continued)

- 2026-09-16: Real-hardware validation on wks01, for the **single-host**
  case only. A real sandbox went `ready` with a genuine segment IP, and the
  host-side artifacts were confirmed directly: Internal vSwitch created,
  `New-NetNat` bound with a real `/29` block, the VM moved onto the segment
  at allocation time, the guest addressed from the segment, a real command
  executed successfully through the full pipeline, and both switch and NAT
  torn down correctly on sandbox deletion (pool reconciler then
  auto-replenished `min_ready`). This resolves risk 2 above in practice —
  no `New-NetIPAddress` timing failure was observed across repeated runs —
  and is treated as closed. **Risk 1 (`New-NetNat` one-instance-per-host)
  remains open and unverified**: the follow-up attempt to test a *second*
  concurrent `New-NetNat` on the same host (see ADR-0022's Open Risks) was
  blocked by an unrelated two-agents-one-host topology gap before it could
  reach a second `New-NetNat` call, so this specific risk is neither
  confirmed nor refuted by this session. It still must be verified on real
  hardware — either a second sandbox on the same single-agent host, or a
  genuine second host — before this feature is trusted in production for
  more than one concurrent sandbox per Hyper-V host.

  Also fixed this session, found while setting up the above:
  `checkTemplateNotAttached`'s guard (see `pkg/providersdk/providers/
  hyperv/driver.go`) went through two rounds of real false positives —
  `Get-VHD.Attached` is true whenever *any* running differencing child has
  an open read handle on the template, not just when the template's own VM
  is running, which permanently blocked a second pool's legitimate clone
  when sharing a template with a first pool's healthy running resource; and
  the first fix (`Get-VMHardDiskDrive`) ignored VM power state entirely,
  matching the template's own (stopped) VM. The guard now checks
  `Get-VMHardDiskDrive` filtered to VMs in the `Running` state, matched
  against the template's own disk path — this is unrelated to network
  isolation itself but was found and fixed in the course of validating it.

- 2026-09-22: **Open Risk 1 (`New-NetNat` one-instance-per-host) closed —
  disproven on real hardware.** Tested directly on wks01 (Windows 10 Pro)
  with raw PowerShell rather than a full daemon/agent/sandbox cycle, since
  the question is purely about what Windows' NAT implementation permits:
  two Internal vSwitches, each given a host vNIC address in its own `/29`,
  each with its own `New-NetNat` over that `/29`. Both succeeded and
  coexisted (`10.250.90.0/29` and `10.250.91.0/29` live simultaneously).
  This is exactly the object shape `hyperv.CreateSegment` creates per
  sandbox, so the per-sandbox NAT design is sound as written and the
  shared-NAT fallback this ADR sketched is not needed.

  The fallback was verified as available anyway, in case a future host or
  Windows build behaves differently: a single NAT over `10.250.0.0/16`
  coexists with two separate per-sandbox switches addressed inside that
  range.

  Scope of what this does and does not establish: it proves the NAT and
  switch objects can be created and coexist. It does not exercise guest
  traffic through two concurrent NATs, and the multi-sandbox path still
  has not been driven end to end through the daemon on real Hyper-V
  hardware — that remains the outstanding validation for this feature.
