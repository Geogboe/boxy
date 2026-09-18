# Plan 1c Final-Review Fix Addendum

**Status:** Approved, implementing as an expanded fix round on `feat/224-overlay-network-plan-1c` before merge.

**Context:** Plan 1c's final whole-branch review (2026-09-14) found two Critical
defects that block merge, both requiring design decisions rather than
mechanical patches. This addendum records the agreed design for both, plus
the smaller Important findings folded into the same fix round. It supersedes
nothing in the base design spec (`2026-09-10-overlay-network-fabric-design.md`)
but does supersede parts of ADR-0012 and ADR-0013 — see below.

---

## C1: Capability advertisement (fixes the devfactory hard-fail)

**Problem:** `ensureNetworkSegment`'s `NetworkIsolatingAllocator` type-assertion
on `*AgentProvisioner` always succeeds (it implements the interface
unconditionally), and `agentsdk.NetworkIsolatingAgent` is implemented
unconditionally by both `EmbeddedAgent` and `RemoteAgent`. The real
`providersdk.NetworkIsolator` capability check only happens three layers
down, inside `EmbeddedAgent.CreateSegment`'s own type assertion on the
resolved driver — and it returns a hard error, not a skip signal. A remote
agent's driver can't be type-asserted from the daemon at all, so this can't
be fixed by checking locally.

**Design:** Agents advertise, at registration, which provider types they can
actually isolate. The daemon consults this before ever calling
`CreateSegment`, treating "not advertised" as skip-not-error — matching how
`AgentInfo.Providers` already advertises which provider types an agent
handles at all.

**Mechanics:**

1. `pkg/agentsdk/agent.go`: `AgentInfo` gains `NetworkIsolatingProviders
   []providersdk.Type` — the subset of `Providers` this agent can actually
   isolate. Empty/nil means none (the correct default for devfactory-only
   agents and any agent not offering an isolation-capable driver).
2. `EmbeddedAgent`'s constructor computes this by type-asserting its own
   `driver(provider)` result for each entry in `Providers` against
   `providersdk.NetworkIsolator`, once, at construction — no wire cost, this
   process already has the real driver instances.
3. For `RemoteAgent`: the `boxy agent serve` process does the identical
   check against its own local drivers at startup, and reports the result
   as part of its existing registration handshake (the token-exchange /
   initial `AgentInfo` message already carries `Providers` — add
   `NetworkIsolatingProviders` alongside it; this is additive to an existing
   message, not a new RPC).
4. `pool.AgentRegistry` exposes a way to check "does agent X advertise
   isolation for provider type Y" (e.g. a method on the registered
   `agentsdk.Agent`'s `Info()`, or a registry-level helper — implementer's
   call, keep it minimal).
5. `internal/pool/provisioner_agent.go`'s `AgentProvisioner.CreateSegment`
   checks this BEFORE calling `agent.(agentsdk.NetworkIsolatingAgent)` /
   `isolator.CreateSegment(...)`. When the agent doesn't advertise support
   for the resolved driver type, return a distinguishable, named sentinel
   error (e.g. `sandbox.ErrNetworkIsolationUnsupported` or similar — pick a
   package that both `internal/pool` and `internal/sandbox` can reference
   without introducing a cycle; check current import direction first) rather
   than a generic `fmt.Errorf`.
6. `internal/sandbox/manager.go`'s `ensureNetworkSegment` treats that
   sentinel as "skip silently, no segment for this resource" (matching
   Global Constraint #3) via `errors.Is`, and continues to treat every OTHER
   error from `CreateSegment`/`AttachToSegment` as a hard failure, unchanged.

**Explicitly NOT part of this fix:** no change to `providersdk.NetworkIsolator`
itself, no change to the proto `Command`/`CommandResult` oneofs (Plan 1b),
no change to `agentsdk.NetworkIsolatingAgent`'s three-method interface. This
stays entirely in the registration/capability-check layer.

**New regression test required** (the review's own top recommendation):
a real `AgentProvisioner` + a registered agent whose driver does NOT
implement `NetworkIsolator` (i.e., a devfactory-shaped agent, or a fake with
`NetworkIsolatingProviders` deliberately empty) must allocate successfully
with zero segments recorded — this is the exact combination no existing test
in Plans 1a/1b/1c reaches.

---

## C2: Hyper-V guest addressing after segment attach

**Problem:** Allocation order is `Allocate(...)` (which calls
`PersonalizeGuest(..., ApplyNetwork: true)`, applying the pool's declared
`network.range`/`static_ip` against the pool's original switch) followed by
`ensureNetworkSegment(...)` (which calls `AttachToSegment`, moving the VM
onto the isolated segment switch via `Connect-VMNetworkAdapter`). Nothing
re-addresses the guest for the segment's own subnet. Hyper-V's Internal
vSwitch issues no DHCP, so the guest is left either on a stale, wrong-subnet
static address or on APIPA.

**Design decision (confirmed with the project owner):** extend #358's
existing principle ("don't configure a network the guest won't end up on")
to cover segments. Since isolation is automatic and universal for every
isolation-capable Hyper-V pool (Global Constraint, no opt-out), the pool's
declared `network.range`/`static_ip` is now *never* the guest's real,
final network for such a pool — the segment always is. Rather than thread a
target-CIDR override through the generic `providersdk.GuestPersonalizer`/
`agentsdk.NetworkIsolatingAgent` layers (which would leak a Hyper-V-specific
concept into provider-neutral interfaces), the fix stays entirely inside
`hyperv.Driver`: **`AttachToSegment` itself assigns the guest's IP address**,
sourced from the segment's own ledger entry, using the exact same low-level
mechanism `applyRangeIP`/`applyStaticIP` already use
(`assignGuestIP(ctx, exec, guestOS, ip, prefix, gateway, dns)` — already
idempotent and self-verifying, see its existing doc comment).

Consequently, `PoolSpec.Network` (Hyper-V's `range`/`static_ip`/
`prefix_length`/`default_gateway`/`dns_servers`) becomes dead code for every
Hyper-V pool: the segment supplies the guest's real address unconditionally,
regardless of what the pool declares. **Per the project owner's explicit
decision, this is removed entirely, not just deprecated** — no pool needs
it anymore given isolation has no opt-out.

**Mechanics:**

1. `pkg/providersdk/providers/hyperv/network_isolation.go`'s segment
   allocation record (`segmentAllocation`, already carries `CIDR`/`Gateway`/
   `SwitchName` per Plan 1a) needs one more derived value: a guest-assignable
   host address within the `/29` block (the gateway already occupies the
   first usable address — e.g. `.1` — so the guest takes the next one, e.g.
   `.2`; since each segment is single-tenant today, one address is enough —
   don't build multi-host allocation within a segment, that's out of scope).
2. `AttachToSegment(ctx, providerResourceID, ref)` — after the existing
   `Connect-VMNetworkAdapter` call succeeds — opens a guest exec session
   (same `psdirect`/`vmsdk.GuestExec` mechanism `personalizeGuestLocked`
   already uses), reads `guestOS`/bootstrap credential from VM notes exactly
   as `personalizeGuestLocked` does (`d.readNotes`, `d.resolveBootstrapCredential`
   — reuse these directly, don't reimplement), and calls
   `d.assignGuestIP(ctx, exec, guestOS, <segment host address>,
   strconv.Itoa(segmentPrefixLen), alloc.Gateway, "")` (no DNS servers — a
   segment is an isolated network with no external resolution needed today;
   revisit only if a concrete need arises).
3. Linux guests: `assignGuestIP` already returns
   `guestIPUnsupportedOnLinux(...)` for Linux — that's the correct behavior
   here too; don't special-case it further. A Linux guest on an isolated
   Hyper-V segment needs its own future mechanism (cloud-init or similar),
   out of scope for this fix.
4. Idempotency: `AttachToSegment` may be called more than once for the same
   resource/segment pair (retry after a partial failure). `assignGuestIP`'s
   own idempotency (already handles re-applying to an already-configured
   guest) covers this — no new guard needed here.

**Removal (PoolSpec.Network and its supporting code):**

- `pkg/providersdk/providers/hyperv/config.go`: remove `NetworkConfig` type,
  the `Network *NetworkConfig` field on the pool config, and its `validate()`
  method.
- `pkg/providersdk/providers/hyperv/ledger.go`: this is the ADR-0012
  range-mode IP-allocation ledger (distinct from Plan 1a's segment ledger in
  `network_isolation.go` — do not confuse the two, do not touch the segment
  ledger). Remove it entirely along with `reserveAddress`,
  `applyRangeIP`/`applyStaticIP`/their call sites in `personalizeGuestLocked`,
  and `ledgerEntry`.
- `pkg/providersdk/providers/hyperv/networkrange.go` +
  `networkrange_test.go`: remove `NetworkRangeReporter`'s implementation —
  it existed solely to validate `NetworkConfig.Range`/`static_ip` against a
  discovered real switch range, which no longer applies once that config is
  gone. Removing this drops `hyperv.Driver`'s implementation of
  `providersdk.NetworkRangeReporter` — confirm nothing else in the codebase
  depends on that capability being present (grep for
  `NetworkRangeReporter` call sites outside this package before deleting).
- `pkg/providersdk/providers/hyperv/driver.go`: remove the `cc.Network`/
  `cc.Switch` range-validation block in `Create` (the `d.validateNetworkRange`
  call site) and the `applyStaticIP`/`applyRangeIP` calls inside
  `personalizeGuestLocked` — `PersonalizeGuest` for Hyper-V no longer applies
  any pool-declared address; it continues to rotate/verify credentials only
  (unchanged), and segment-sourced addressing happens in `AttachToSegment`
  instead, per above. **`config.Switch` itself is NOT removed** — a pooled
  VM still needs some switch to exist on before a segment exists (created
  via the existing, unchanged `Create` path); only the guest-side
  range/static-IP application is removed.
- `internal/config/schema/providers/hyperv.config.schema.json`: remove the
  `network` sub-schema.
- Update `docs/adr/0012-hyperv-range-based-ip-allocation.md` and
  `docs/adr/0013-hyperv-network-range-reporter.md`: add a dated change-log
  entry to EACH (per this repo's ADR convention — never rewrite ADR history,
  append a note) stating both are superseded by segment-sourced addressing
  as of #224/Plan 1c, and pointing to this addendum and the new ADR-0021
  update (below) for why.
- `docs/adr/0021-network-isolation-driver-capability.md`: add a change-log
  entry describing this addendum's two fixes (capability advertisement,
  segment-sourced Hyper-V addressing) and resolving its own "Open risks for
  Plan 1c" section — see the next item.

**Resolving ADR-0021's carried-forward risks (finding I1):** one of the two
named risks (`New-NetIPAddress` immediately after `New-VMSwitch` — adapter
materialization timing) is directly exercised by this fix's own new
`assignGuestIP` call inside `AttachToSegment`, since that now runs guest-exec
work immediately after the switch/NAT setup in `CreateSegment`. If this
surfaces as a real timing issue during implementation, fix it in the same
pass (e.g. a bounded retry/poll before the guest-exec step) and record the
resolution in ADR-0021's change log — don't defer it again. The other risk
(`New-NetNat`'s possible one-instance-per-host limitation) remains
unverifiable from this dev host; re-record it explicitly in ADR-0021's
change log as still open rather than letting it silently vanish again.

---

## Other findings folded into this same fix round

- **I2** — add `var _ sandbox.SegmentDestroyer = (*pool.Manager)(nil)` (in a
  `pool_test` file per the same import-cycle reasoning already documented in
  `provisioner_agent_isolation_test.go`) pinning the plain-string signature
  pairing so future drift fails loudly, not silently.
- **I3** — `AgentProvisioner.CreateSegment` rejects an empty `SegmentRef`
  returned without an error as an error itself (the validation
  `agentsdk.NetworkIsolatingAgent`'s doc comment explicitly assigned to this
  layer).
- **I4** — persist `sb.NetworkSegments` immediately after a successful
  `CreateSegment` (before `AttachToSegment`), in both `CreateFromPool` and
  `AddFromPoolWithPackages`'s loops, so a real host object is never orphaned
  by an ordinary later error in the same loop iteration or a later resource.
- **I5** — in `internal/sandbox/deleter.go`'s `cleanupSandbox`, distinguish
  "segment's agent is unavailable" (log and skip that segment, matching the
  existing resource-path force-orphan escape hatch's spirit — the future
  orphan-sweep owns real cleanup) from a genuine `DestroySegment` failure
  (still a hard error, unchanged) — a permanently-decommissioned agent must
  not block that sandbox's deletion or stall the whole reconciler tick.
- **I6** — update `AGENTS.md`'s `NetworkIsolator` bullet (drop the "control
  plane still calls none of it" language, it's now false), add change-log
  entries to ADR-0021 (see above), and add an entry to
  `docs/current-delivery-notes.md` for this feature landing.
- Minor findings (persist-guard asymmetry, unwrapped vs wrapped error
  context, dead test fields) — fold in opportunistically if the implementer
  is already touching the relevant lines; not worth a dedicated pass.

## Explicitly out of scope for this fix round

- Docker needs no equivalent fix (its IPAM already assigns a correct address
  on the new bridge network — confirmed in the original review).
- Segment orphan-sweep (crash-window gap) remains a deliberately deferred
  follow-up, unchanged from the base plan.
- Decisions 2–4 of the design spec (mesh, JIT access, AccessBroker) are
  unrelated and untouched.
