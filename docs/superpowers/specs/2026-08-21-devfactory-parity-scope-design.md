# Design: devfactory Provider Parity — Scoped (#181)

## Context

#181 is a deliberately open-ended follow-up from #173: "bring devfactory to
full parity as a general-purpose provider stub." The issue itself warns
against treating that as "implement every optional interface every real
driver has" — it asks for a scoping decision, made once, in writing, before
touching code. This document is that decision.

devfactory (`pkg/providersdk/providers/devfactory/`) is a reference/testing
double for `providersdk.Driver`, not a Hyper-V simulator. Its stated purpose
(package doc comment) is exercising Boxy's own pipeline — lifecycle,
persistence, latency, failure, availability, streaming plumbing — without
claiming fidelity to any specific real provider. That framing does the
scoping work for us: a capability belongs in devfactory when it's
provider-*neutral* plumbing a consumer would want to exercise against a
reference double; it doesn't belong when it only exists to model one real
provider's specific mechanics.

### Capability comparison (docker / hyperv / devfactory, before this change)

| Capability | docker | hyperv | devfactory |
|---|---|---|---|
| `ResourceLister` | yes (label filter) | yes (name-prefix query) | no |
| `StreamingDriver` | yes | yes | yes |
| `AvailabilityReporter` | no | yes (live host query) | yes, but can't express zero (see below) |
| `GuestPersonalizer` | no | yes (credential rotation) | no |
| Typed errors (`ErrorTyper`) | no | yes (`CapacityError`, `OrphanedResourceError`) | no |
| Cleanup on `Create` failure | yes (`deleteBestEffort`) | yes (`deleteBestEffort` + `createFailure`) | no — see "ctx-cancellation gap" below |

## Decisions

### 1. Add `ResourceLister` — in scope

`List` is "enumerate what I currently manage," reflected straight from
whatever bookkeeping the driver already keeps — docker's container labels,
hyperv's VM name prefix, devfactory's own JSON store. It carries no
provider-specific mechanics; every driver that keeps any inventory at all
can trivially implement it, and both real drivers already do. devfactory
already persists exactly the record this needs (`resourceRecord.ID`,
`.State`) in `store.go`. Implemented as a direct reflection of the store,
sorted by ID for deterministic test/CLI output (map iteration order isn't).

### 2. Fix `AvailabilityReporter`'s zero-value ambiguity — in scope

Flagged in #181's own follow-up comment: `Config.AvailableMemoryMB == 0`
means "unlimited" (matching the zero-value-friendly pattern the rest of this
config struct already uses — `FailCreate`'s zero value means "don't fail").
That means the config can't express the one number a consumer would
actually want to simulate: zero/insufficient capacity, to exercise
`CapacityError`-equivalent handling against a reference driver instead of
only against real Hyper-V or hand-built fakes.

**Rejected: change `AvailableMemoryMB` to `*int64`.** A pointer would let
`nil` preserve today's "unset ⇒ unlimited" default while an explicit `&0`
means real zero. But it's a type change on a field in a public package —
source-breaking for anything constructing `Config{AvailableMemoryMB: N}` as
a Go literal, and every AGENTS.md instruction on this project says API
changes must stay additive. The capability doesn't require it.

**Accepted: add `AvailableMemoryZero bool`.** Its zero value (`false`,
matching every other `Fail*`-style flag in this struct) reproduces today's
behavior exactly — no config that omits the field changes meaning, no Go
literal needs editing, no migration. Set `true`, and `Availability()`
reports exactly `0` regardless of `AvailableMemoryMB`. `AvailableMemoryMB`
itself is untouched: still `int64`, still "0 ⇒ unlimited" when
`AvailableMemoryZero` is false.

### 2a. Fix the unlimited sentinel value — in scope, same follow-up comment

The same comment named a second, independent defect: the *current*
"unlimited" sentinel is `math.MaxInt64`. `hyperv.Driver.Create` converts a
`MemoryMB` value to bytes via `MemoryMB * 1024 * 1024`
(`driver.go:259`); the same conversion applied to `MaxInt64` overflows
silently. It's also a live, visible defect today, not just a latent one: the
heartbeat dashboard (`internal/server/ui.go:199`,
`formatMemoryMB`) renders whatever `Availability()` reports as
`"<N> MB free"` with no special-casing for "unlimited" — so an unconfigured
devfactory pool renders as `"9,223,372,036,854,775,807 MB free"` on the
dashboard today.

Replaced with `unlimitedMemoryMB = 1_000_000_000_000` (1e12 MB, ~1 EB) —
comfortably under `math.MaxInt64/(1024*1024)` (~8.8e12) so the same
byte-conversion pattern can't wrap, while still reading unambiguously as "no
real host has this much," not a plausible real figure. This is the
interactive-validation target for this change: a devfactory pool with
`available_memory_zero: true` should render `"0 MB free"` on the dashboard,
and an unconfigured one should render a finite, sane number instead of the
current absurd one.

### 3. Add typed-error simulation for `Create` — in scope

`CapacityError` and `OrphanedResourceError` (`pkg/providersdk/errors.go`)
are provider-neutral — defined in `providersdk` itself, not hyperv-specific,
existing specifically so any driver can report them and any consumer
(pool provisioner quarantine logic, RemoteAgent/gRPC error reconstruction,
CLI error rendering) can handle them via `ErrorTyper` without knowing which
driver produced them. Today the only way to exercise that handling is
against real Hyper-V or a hand-built `fakeProviderDriver`
(`internal/pool/provisioner_driver_test.go`) — devfactory, the driver
explicitly meant to be a reference double for this kind of plumbing, can't
produce either type.

Added `Config.FailCreateAs string`, values `""` (default, unchanged
behavior), `"capacity"`, `"orphaned_resource"`. `FailCreate` (plain error)
still takes precedence if both are set, preserving its existing simple
behavior for callers who don't care about the typed-error path.

- **`"capacity"`**: `Create` returns `*providersdk.CapacityError` with
  `RequestedMemoryMB` fixed at `simulatedMemoryRequestMB = 2048` — matching
  `hyperv.Driver`'s own default `CreateConfig.MemoryMB`
  (`driver.go:217-218`), so a consumer sees realistic numbers without
  configuring one — and `AvailableMemoryMB` read from the driver's own
  `Availability()` (so combining this with `AvailableMemoryZero: true` or a
  low `AvailableMemoryMB` produces a self-consistent insufficient-capacity
  error). Deliberately *not* derived as `available + 1`: in the unlimited
  case that's `unlimitedMemoryMB + 1`, and a consumer that reproduces
  hyperv's MB→bytes conversion on it would be right back at the overflow
  this change exists to fix elsewhere. A fixed requested value sidesteps
  that entirely.
- **`"orphaned_resource"`**: `Create` writes a store record in `"creating"`
  state — the same record shape a normal in-flight `Create` writes before
  its latency wait — but never advances it to `"running"`, then returns
  `*providersdk.OrphanedResourceError{ID: <that record's ID>, ...}`. This
  mirrors what a real orphan actually looks like (something partially
  provisioned, present in the driver's own inventory, not reachable through
  any successful `Create` return) and gives `List()` something concrete to
  surface — a consumer can exercise the full quarantine-and-later-cleanup
  round trip (`internal/pool/provisioner_agent.go:59`'s
  `newQuarantinedResource`, then a later `Delete` on that same ID) against
  devfactory instead of only against real Hyper-V.

No decoding of a real memory request from `cfg` was added to `Create` for
this — `Create`'s `cfg any` parameter still goes untouched by capacity
logic outside the `FailCreateAs` injection path. Modeling a general
capacity-vs-request check for every ordinary `Create` call remains out of
scope; the existing doc comment on `Availability()` already explains why
(no per-call memory-request modeling), and that reasoning is unchanged by
this addition — `FailCreateAs` is an explicit failure-injection knob, not
inferred from an unrelated config field.

### 4. Cleanup on `Create` cancellation — in scope, small

Both real drivers clean up after a failed `Create` (`deleteBestEffort` in
each). devfactory's `Create` had one failure path with no cleanup at all: if
the caller's `ctx` is cancelled during the simulated latency wait, `Create`
returns `ctx.Err()` — but the `"creating"`-state record it already wrote to
the store is never removed. Unlike a normal failed `Create`, this record is
unreachable through any returned ID (`Create` never returns one on this
path) and isn't reported as an orphan either, so it would sit in the store
forever with no consumer capable of ever finding or destroying it — silently
skewing `ResourceCount()`/`List()` results in any test that exercises
cancellation. Fixed by deleting the record before returning `ctx.Err()`,
matching the real drivers' cleanup-on-failure convention.

### Non-goals

- **`GuestPersonalizer` / guest credential bootstrap & rotation.** Excluded
  deliberately, not by oversight. Unlike every other capability compared
  above, `GuestPersonalizer` has exactly one implementation
  (`hyperv.Driver.PersonalizeGuest`) and no second real provider to validate
  a simulated contract against — docker doesn't implement it either, because
  containers don't need password rotation. Simulating it in devfactory would
  mean inventing a credential-rotation contract with nothing to check it
  against, which is precisely the kind of unscoped work #181 was filed to
  prevent. ADR-0010's one-time-delivery guarantee is specific to Hyper-V's
  actual security model (server-owned bootstrap credential, mTLS-authorized
  agent resolution, PowerShell Direct/SSH rotation) — reproducing it here
  would be Hyper-V-specific behavior wearing a generic-looking interface. If
  a concrete testing need for this emerges later (e.g. end-to-end tests of
  ADR-0010's delivery guarantee that shouldn't require real Hyper-V), that's
  a new, narrowly-scoped issue with its own reasoning about what contract to
  simulate — not something to fold into this pass.
- **PowerShell VM lifecycle, host health checks, PowerShell Direct.**
  Genuinely Hyper-V-specific mechanics with no provider-neutral shape to
  extract. Per the project's own framing (AGENTS.md, "devfactory is the
  generic deterministic provider simulator... [not] claiming fidelity to a
  real provider"), devfactory is not the place to grow a second, fake
  implementation of Hyper-V's host-health probe or PowerShell Direct
  transport. A future `hyperv-sim` package (already anticipated in
  AGENTS.md) is the intended home for anything that specifically needs to
  imitate Hyper-V.
- **Decoding a real memory request in `Create` to enforce capacity
  generically** (as opposed to the explicit `FailCreateAs: "capacity"`
  injection knob above). See Decision 3.
- **Changing `AvailableMemoryMB`'s type or default meaning.** See Decision 2.

## Testing

- `List()`: empty store, single resource, multiple resources (assert sorted
  order), a resource left behind by `"orphaned_resource"` injection is
  included.
- `Availability()`: `AvailableMemoryZero: true` reports `0` regardless of
  `AvailableMemoryMB`; unset reports `unlimitedMemoryMB`, not
  `math.MaxInt64`; an explicit positive `AvailableMemoryMB` still reports
  that value unchanged.
- `FailCreateAs: "capacity"`: returns `*providersdk.CapacityError` with the
  fixed `RequestedMemoryMB` and an `AvailableMemoryMB` that reflects
  `AvailableMemoryZero`/`AvailableMemoryMB` config; `errors.As` succeeds.
- `FailCreateAs: "orphaned_resource"`: returns `*providersdk.OrphanedResourceError`;
  the returned `ID` is present in the store in `"creating"` state; `List()`
  surfaces it; a subsequent `Delete(id)` removes it (the round trip a real
  quarantine-and-cleanup flow depends on).
- `FailCreate` still takes precedence over `FailCreateAs` when both are set.
- ctx-cancellation cleanup: cancel `ctx` mid-latency, assert `ResourceCount()`
  returns to 0 and the ID is absent from `List()`.
- Registration/interface-compliance: `var _ providersdk.ResourceLister = (*Driver)(nil)`
  alongside the existing `AvailabilityReporter` assertion.

## Interactive validation

Use the devfactory VM profile (safe, simulated — no real Hyper-V) with
`available_memory_zero: true` configured on a pool, run `boxy serve`, and
check the heartbeat dashboard renders `"0 MB free"` for that pool's provider
— and that an otherwise-unconfigured devfactory pool renders a finite
number, not the previous `MaxInt64`-derived garbage figure. This is a real,
currently-broken visual before this change and a concrete pass/fail after
it.
