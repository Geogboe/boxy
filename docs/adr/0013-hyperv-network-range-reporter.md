# ADR 0013: Hyper-V `NetworkRangeReporter` — validate declared ranges against switch reality

Status: Accepted

## Context

ADR-0012 lets a pool declare `network.range`, a CIDR the per-agent ledger
allocates addresses from at allocation time. That range is entirely
operator-declared — Boxy never checks it against what the named
`config.switch` actually has bound to it on the host. Two failure modes
follow directly from that gap:

- A typo or stale value in `network.range` produces VMs with addresses that
  don't route on the real switch.
- Worse, a declared range that overlaps a subnet already in use outside
  Boxy's knowledge (DHCP scope, another workload, a second Boxy pool on the
  same host with a different declared range) collides silently — nothing
  in the existing design would surface it before or after the fact.

ADR-0012 named this as Non-goal/#223, a deliberate fast-follow rather than
in-scope for Phase 1. This ADR implements it.

## Decision

### A new `providersdk.NetworkRangeReporter` capability, not a new subsystem

```go
type NetworkRange struct {
    CIDR      string // canonical network-address CIDR, e.g. "203.0.113.0/24"
    Gateway   string // the switch's own host-side address within CIDR, when known
    NATBacked bool   // best-effort: true only if a NAT rule was positively matched
}

type NetworkRangeReporter interface {
    NetworkRanges(ctx context.Context, switchName string) ([]NetworkRange, error)
}
```

Detected by type assertion, the same optional-capability pattern as
`AvailabilityReporter`, `ResourceLister`, `GuestPersonalizer`, and
`RelativePathResolver` — no `agentsdk`/`Agent`-level "hardware inventory"
concept is introduced. This stays a `Driver`-scoped question because only
the driver knows how to ask its own provider's host about live switch
state; a generic inventory layer above `Driver` would have to reinvent
that per-provider knowledge anyway for zero benefit.

### Discovery mechanism: the switch's host vNIC, not `Get-VMSwitch`

A Hyper-V virtual switch object itself carries no IP address. For an
Internal or Default switch, Hyper-V creates a host-side network adapter
named `vEthernet (<switch name>)`; that adapter's own IPv4 address and
prefix (`Get-NetIPAddress -InterfaceAlias 'vEthernet (<name>)'`) is exactly
the answer to "what range does this switch actually serve" — it's the
gateway address for every guest on that switch's range.

`Get-NetNat` has no field naming which switch it backs — its
`InternalIPInterfaceAddressPrefix` values are cross-checked in Go against
each discovered address's own network prefix instead of trusted as the
primary source. This is deliberately a secondary/best-effort signal,
surfaced only as `NetworkRange.NATBacked`: a mismatch or a `Get-NetNat`
query failure doesn't invalidate the vNIC-derived `CIDR`/`Gateway`, which
remain the authoritative discovery result either way.

An External switch's host adapter carries the physical NIC's own LAN
address instead of a per-VM range. This ADR does not special-case that —
it's simply another discovered range that the containment check below
either matches or (correctly) doesn't; no attempt is made to classify
switch type up front and reject External switches outright, since a
legitimate External-switch pool with a `network.range` scoped to the real
LAN subnet is a valid configuration this same logic already handles
correctly.

### Containment, not equality

`network.range` only needs to fall *within* a discovered range, not equal
it. The expected real configuration is an operator carving a sub-range
(e.g. `203.0.113.128/25`) out of a switch's wider NAT/DHCP scope (e.g.
`203.0.113.0/24`) specifically to avoid colliding with the switch's own
DHCP pool or another consumer of that same subnet — an equality check
would reject exactly that, the most sensible configuration to have.

### Fail-vs-warn is decided by determinacy, not by preference

- **Positive contradiction** — discovery succeeded and returned at least
  one range, and the declared range falls inside none of them — is a hard
  `Create` error. This is the actual typo/drift case #223 exists to catch.
- **Indeterminate** — the PowerShell query itself failed, or came back
  with zero discovered ranges (a Private switch has no host vNIC at all;
  an unusual host configuration might return nothing parseable) — is not
  an error. `Create` logs a warning and proceeds exactly as it did before
  this capability existed.

This split matters because this development host cannot run Hyper-V (see
AGENTS.md's Lessons Learned): the PowerShell this ADR adds ships validated
only against injected `psExec` fakes, never a real switch. Treating a
query that might fail for host-specific reasons having nothing to do with
operator error as a hard gate on all provisioning would make every
`Create` on every real host hostage to a script this repository cannot
fully verify pre-merge. An unverifiable check should be a warning, not a
gate, until it's proven against real Hyper-V.

### Validation runs inside `Driver.Create`, not at pool registration/reconcile time

The originating issue described validating "at pool registration/reconcile
time," but that layer has no access to the information needed:

- `config.PoolSpec.Config` is `map[string]any`, decoded into a
  provider-specific `CreateConfig` only inside `hyperv.Driver.Create`
  itself (`decodeCreateConfig`). Nothing in `internal/pool` or
  `pkg/policycontroller` knows `network.range` or `config.switch` exist —
  teaching that layer to decode per-provider config would be new coupling
  this codebase has specifically avoided (see `AGENTS.md`'s `pkg/` vs.
  `internal/` split).
- In the real deployment topology (ADR-0005), the Hyper-V driver runs on a
  remote agent, not the daemon that owns pool reconciliation. A
  daemon-side check would need a new proto message, a new `RemoteAgent`
  method, and `remoteclient`/`remote.go` wiring — scope `#223` never asked
  for and this ADR does not add.

`Driver.Create` is the one place that already has `cc.Network.Range` and
`cc.Switch` in the same scope with zero new plumbing, and it's called
exactly once per resource provision — the natural point to fail fast
before any host resources (`New-VHD`/`New-VM`) are committed. The check
runs immediately after the existing `cc.Network.validate()` call and
before `reserveMemory`, so a misconfigured pool fails cheaply, without
reserving host memory it will never use.

This still satisfies "pool reconciliation surfaces a clear error" from the
issue's acceptance criteria: a `Create` error already propagates through
`Manager`'s existing `recordProvisionFailure` + `lifecycle_error` resource
property + capped exponential backoff (see `internal/pool/manager.go`).
No second, parallel validation/surfacing path was built.

### `devfactory` does not implement this capability

Consistent with the devfactory-parity scope decision already recorded in
`AGENTS.md` (#181) for `GuestPersonalizer`: `NetworkRangeReporter` has
exactly one real implementation (`hyperv.Driver`) and no second provider to
validate a simulated "discover a switch's range" contract against.
Simulating it in devfactory would mean inventing Hyper-V-specific
network-discovery semantics wearing a generic-looking interface — the same
reasoning that kept `GuestPersonalizer` out of devfactory. Not implemented
here; revisit only with a concrete testing need of its own.

### Validation covers both network modes, not just `network.range`

The issue that motivated this ADR talks about `network.range` specifically,
but `NetworkConfig.StaticIP` (`static_ip` mode, ADR-0009/#201) is exactly as
exposed to a typo'd address or host network drift as `range` mode is — both
declare a fixed IPv4 value the guest must actually be reachable at over the
named switch. `validateNetworkRange` therefore checks whichever of
`Range`/`StaticIP` is set (`NetworkConfig.validate` already guarantees
exactly one is, when `Network` is non-nil at all), treating a declared
`StaticIP` as a `/32` for the same containment check `Range` gets. There is
no reason for one mode to get this protection and not the other.

### No config surface change

No new `hyperv.Config`/`CreateConfig` field is introduced — no operator
opt-in or opt-out toggle, no schema edit. Validation runs unconditionally
whenever `config.switch` and either `network.range` or `network.static_ip`
are set; there is no case where an operator would want a declared address
silently untested against the switch it's supposed to belong to. If a real
deployment surfaces a legitimate reason to skip the check (e.g. a switch
type this ADR's discovery mechanism can't yet handle correctly), that's a
config-surface decision for a future, scoped change — not speculatively
added here.

## Non-goals / explicitly out of scope

- **Auto-populating `network.range` from the discovered switch range**
  (ADR-0012's own stated stretch goal for #223) is deliberately deferred,
  for a sharper reason than "extra work": `reserveAddress` computes "taken"
  addresses by scanning ledger entries sharing the same `RangeKey` (the
  declared CIDR string, verbatim). If `Range` were auto-discovered instead
  of operator-declared, a host whose NAT/DHCP prefix changes across a
  reboot (anecdotally reported for the Windows Default Switch) would
  change every subsequently-created VM's `RangeKey`, so their "taken"
  scan would exclude every VM created under the old prefix — silently
  reintroducing the exact address collision #222/ADR-0012 exists to
  prevent, just moved to a new boundary. Validating an operator-declared,
  stable `RangeKey` against live discovery has no such hazard; auto-
  populating it from that same live discovery does, without a `RangeKey`
  migration story this ADR does not attempt to design.
- **Multi-switch / multi-pool cross-validation.** This ADR validates one
  pool's declared range against one switch's discovered range(s) at
  `Create` time. It does not check whether two pools on the same host
  declare overlapping ranges against the same or different switches —
  that gap is already recorded in ADR-0012's Consequences and is
  unaffected by this change.
- **Classifying switch type up front** (Private/Internal/External) to
  reject or special-case one kind. See "Discovery mechanism" above.
- No `providersdk`/`agentsdk` interface changes beyond the one new
  optional capability. `Driver.Create` is the only call site.
- No CLI surface change, no bundled-skill or `docs/api.md` edit — same
  reasoning ADR-0012 recorded for its own config surface: neither
  currently documents provider `config:`-level fields.

## Consequences

- `NetworkRange.Gateway` and `NetworkRange.NATBacked` are discovered but not
  consumed by `validateNetworkRange`, the only in-tree caller — it reasons
  about `CIDR` alone. That's expected, not dead weight: `NetworkRanges`
  itself, not `validateNetworkRange`, is the capability `#223`'s acceptance
  criteria actually asks for ("discover the switch's actual NAT/internal
  range"), and `Create`'s validation is just its first internal consumer.
  Concretely, ADR-0012's `reserveAddress` still only excludes the
  *operator-declared* `NetworkConfig.DefaultGateway` from allocation — a
  pool declaring `network.range` with no `default_gateway` set can still
  hand a VM the switch's own host-vNIC address, discovered here but not
  wired into that exclusion. Collecting `Gateway`/`NATBacked` now is
  deliberate: they're the obvious inputs a future change would need to close
  that gap, or to reject a declared range that isn't actually NAT-backed,
  without a second discovery round-trip — but wiring either into
  `reserveAddress` or into a new validation rule touches the ledger's
  allocation path and is its own scoped change, not folded in here.
- `validateNetworkRange` logs through `slog.Default()` rather than a new
  injected `*slog.Logger` field on `Driver` — no other driver in
  `pkg/providersdk/providers` logs at all today, and adding a logger field
  would mean threading it through `New`, `buildDrivers`, and
  `buildAgentDrivers` for one warning path. `slog.Default()` needs no new
  wiring and keeps this `pkg/` package independent of Boxy's own logging
  setup, at the cost of not honoring whatever handler/level the daemon or
  agent process configured for its own `slog.SetDefault`. Revisit only if
  a second driver needs the same soft-warning pattern.
- A pool with `config.switch` and a declared network address set now
  performs one additional PowerShell round-trip per `Create` call (the same
  driver method a future operator-facing debug command could also call
  directly, though no such command is added here). `validateNetworkRange`
  bounds this call with `d.memQueryTimeout()` — reused rather than adding a
  third "bounded live host query" duration field, the same reuse decision
  `resolveCreatedVMID` already made for its own retry pacing (see that
  function's comment on reusing `d.bestEffortInterval()`). Without a bound
  here, a hung `Get-NetIPAddress`/`Get-NetNat` on a degraded host would
  block `Create` indefinitely on a ctx that may have no deadline of its own
  (e.g. the background reconcile loop's) — the same class of hang
  `checkHostHealth`/`reserveMemory`'s own timeouts in this file already
  exist to prevent.
- A pool filling several resources at once (e.g. `min_ready > 1`) repeats
  this same discovery query once per `Create` call, even though the
  switch's discovered range cannot change between back-to-back calls for
  the same pool config within one fill. No per-switch caching is added:
  the query is cheap relative to `New-VHD`/`New-VM`/`Start-VM` in the same
  `Create` call, and a cache would need its own invalidation story (a
  switch's real range *can* change between fills, e.g. after a host
  network reconfiguration) that isn't worth the complexity for a
  `priority:low` capability. Revisit only if this round-trip is measured to
  matter in practice.
- A pool that previously worked with a declared-address/`config.switch`
  mismatch that happened to not matter in practice (e.g. a switch whose
  real range silently contained the mistake) is unaffected — only a
  genuine, discoverable contradiction now fails `Create`.
- Because this host cannot exercise real Hyper-V, the PowerShell in
  `NetworkRanges` is verified only against injected `psExec` fakes
  covering the happy path, the no-NAT-match path, the empty-result path,
  and the query-failure path — not against a live switch. Retest against
  a real Hyper-V host's `Get-NetIPAddress`/`Get-NetNat` output shape before
  relying on this in production; see AGENTS.md's Lessons Learned for the
  standing caveat this repeats.
