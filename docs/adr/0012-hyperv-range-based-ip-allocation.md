# ADR 0012: Hyper-V range-based IP allocation with a restart-safe ledger

Status: Accepted

## Context

#201 (2026-08) fixed "the guest has no address to reach at all" by letting a
pool declare a single `network.static_ip` in `CreateConfig`, applied inside
the guest during `PersonalizeGuest` via PowerShell Direct. That's correct for
its original single-VM scope, but `StaticIP` is one fixed string on the
pool's `CreateConfig` — every `Create` call for that pool writes the
*identical* address into every VM's `Set-VM -Notes`. Any pool with
`min_ready > 1`, or that preheats multiple VMs before allocation, collides on
the wire. #222 tracks closing this gap; #200/#201 stay closed as correct for
their scope.

This ADR also formally records a design that was previously only captured in
the #222 issue body, per AGENTS.md's own convention ("When decisions are
made, save them as ADR documents in /docs/adr").

## Decision

### Addresses are reserved at allocation time, not provision time

A resource sitting ready-but-unallocated in a pool has no real IP need yet,
and reserving early would waste a scarce range for however long it sits
idle. The existing hook points already fit this without any
`providersdk`/`agentsdk` interface change:

- `Driver.Allocate(ctx, id)` already calls `PersonalizeGuest(ctx, id)`
  (`driver.go`) — the allocation-time hook.
- `Driver.Delete(ctx, id) error` is the one required teardown method every
  provider implements, and the single point every teardown path converges
  on: recycle-by-max-age, drain, sandbox-triggered `DestroyResource`, and
  the orphan sweep in `internal/pool/manager.go`. Releasing an address there
  covers all four paths with no new plumbing.

### Ledger, not VM Notes

Pool-wide address bookkeeping is cross-VM state and doesn't belong split
across N different VMs' `Set-VM -Notes` fields — the mechanism used today
for the single static IP, and for `boxy_guest_os`/`boxy_guest_user`/
`boxy_guest_password_ref`, which are genuinely per-VM identity data. Range
mode writes **no** networking fields to Notes at all; Notes keeps carrying
only those three pre-existing keys. `static_ip` mode is unchanged and still
writes its notes fields exactly as before.

Instead: a `pkg/diskjson.Store[T]` (ADR-0011's existing restart-safe,
atomically-written JSON-blob pattern — the same one devfactory's own store
already uses), keyed by **resource ID** (`pkg/providersdk/providers/hyperv/ledger.go`):

```go
type ledgerEntry struct {
    RangeKey        string   // canonical CIDR, e.g. "203.0.113.0/24"
    PrefixLength    int
    DefaultGateway  string
    DNSServers      []string
    AssignedAddress string   // empty until Allocate reserves one
}
```

- **`Create`**: after the VM is confirmed running and its ID resolved,
  writes a `ledgerEntry` for that ID with `RangeKey`/`PrefixLength`/
  `DefaultGateway`/`DNSServers` copied from `cc.Network`. `AssignedAddress`
  is left empty — no address is chosen yet.
- **`Allocate` → `PersonalizeGuest`**: looks up the ledger entry by id. If
  `AssignedAddress` is already set (a retry after a crash between the
  reservation and the guest-apply step), reuses it — no new address is
  burned. Otherwise it scans every other entry sharing the same `RangeKey`,
  excludes their assigned addresses plus the range's own gateway, picks the
  first free address in the CIDR (skipping the network and broadcast
  addresses), and persists it — all inside one `diskjson.Store.Update`
  callback, so two concurrent `Allocate`s on the same range cannot observe
  the same "next free" address (`Update` holds its lock across the whole
  load-modify-save; a `Load` then `Save` split would not). It then applies
  the address in-guest exactly as `static_ip` mode already does via
  PowerShell Direct.
- **`Delete`**: releases the ledger entry for that ID unconditionally,
  regardless of whether the VM was actually found. Implemented as a
  `defer` right after `Delete`'s existing id-empty check, gated on the
  function returning a nil error — so it fires on both of `Delete`'s
  "confirmed gone" paths (the `__BOXY_NOT_FOUND__` short-circuit and the
  end-of-function success return) without duplicating the release call at
  each one, and without releasing an address for a VM whose teardown is
  still in flight (e.g. `ErrVMBusy`) or failed outright — a later retry of
  `Delete` (recycle backoff, drain retry, the orphan sweep) reaches a nil
  return eventually and releases then. This matters in practice: the
  orphan sweep and a crash-then-recycle both call `Delete` on a VM that is
  *already gone* from Hyper-V (the `__BOXY_NOT_FOUND__` path) with a live
  ledger entry — release must not sit only behind the path that actually
  runs `Remove-VM`, or addresses leak permanently on the path most likely
  to fire in production.

Releasing a ledger entry is just deleting the map key: since "taken" is
computed by scanning live entries for a given `RangeKey` (not a separate
free-list structure), removing the entry *is* the release. No separate
free-list bookkeeping is needed.

### Self-contained per-resource entries, not a normalized range registry

An entry carries its own `PrefixLength`/`DefaultGateway`/`DNSServers`
copied from the pool config at `Create` time, rather than a minimal
`{RangeKey, AssignedAddress}` pair with a separate `RangeKey → {gateway,
dns}` registry looked up at `Allocate` time. `PersonalizeGuest` has no
access to the originating `CreateConfig` — it only has the resource ID — so
the full network config needs to be recoverable at allocation time from
*something* keyed by resource ID.

The self-contained shape is deliberately chosen over a registry, for the
same reason `model.Resource.OriginPool` is a provenance snapshot rather
than a live pointer back to pool config: an entry records the network
config that was in effect when that VM was created. If an operator edits a
pool's declared gateway later, VMs already provisioned keep what they were
created under; only new ones pick up the change. A normalized registry
would either silently break that (retroactively changing an in-flight VM's
recorded gateway) or require inventing a policy for what happens when two
pools declare the same `RangeKey` with different gateways — a
cross-pool-consistency question this shape avoids needing to answer at
all. The modest duplication (a gateway string and a short DNS list per
entry, not secrets) is cheap.

### Range mode trusts the ledger's address, not a Get-VMNetworkAdapter read-back

`PersonalizeGuest` queries `Get-VMNetworkAdapter ... IPAddresses` (`vmIP`)
after applying `static_ip`, and uses that read-back — not the notes-declared
address directly — for `AccessDetails` and for the guest-exec connection
used to rotate/verify the credential. That was safe in `static_ip` mode
because the notes-declared address and the guest-reported address are the
same value by construction (one fixed address, always).

Range mode does **not** reuse that read-back. `applyRangeIP` returns the
address it just reserved and applied, and `PersonalizeGuest` uses that
value directly instead of calling `vmIP` in the range-mode branch.
`IPAddresses` is populated by the guest's integration services and can lag
a fresh `New-NetIPAddress` by several seconds — a read-back immediately
after assigning could observe a stale pre-assignment address (silently
wrong: the caller connects to the wrong VM's address) or an empty list
(`vmIP` turns that into a hard error, failing the whole allocation). The
ledger's own `AssignedAddress`, by contrast, is exactly what was just
written into the guest and has no such lag — it is the source of truth for
range mode, not the guest's self-reported state.

### The ledger lookup is the mode discriminator

`PersonalizeGuest` checks for a ledger entry by id first. If one exists,
range mode applies (reserve-if-needed, then assign in-guest). If none
exists, it falls through to today's `boxy_net_static_ip` Notes check
unchanged. No new Notes flag (e.g. a `boxy_net_mode` key) is introduced to
distinguish the two — the ledger's own presence or absence is sufficient as
the discriminator, so `static_ip` mode is provably untouched by this change
at the Notes/wire level.

### Config surface: `network.range` coexists with `network.static_ip`, mutually exclusive

`NetworkConfig` gains `Range string` (a CIDR, e.g. `"203.0.113.0/24"`).
`StaticIP` becomes optional (still required to be non-empty *unless*
`Range` is set); `validate()` rejects setting both. They coexist rather
than one replacing the other: a genuine single-VM pool has no need for a
range, and `static_ip` already shipped in #201 — breaking it outright to
force `range` even for a one-resource pool would be pure churn. IPv4 only,
matching `static_ip`'s existing IPv4-only scope; a non-IPv4 `Range` is
rejected at validation time. `PrefixLength` remains meaningful only for
`static_ip` mode — `range` mode derives its prefix length from the CIDR
itself, which is authoritative.

### Ledger location: `Config.DataDir`

Host-level `Config` (already the right scope — one Hyper-V host, one
ledger, covering every pool on that host) gains `DataDir string`,
resolved the same way `devfactory.Config.DataDir` is (ADR-0011's
`providersdk.RelativePathResolver`): a relative path resolves against the
boxy config file's own directory when one is known. Unlike devfactory
(whose empty `DataDir` intentionally falls back to a throwaway temp
directory — it's a debug/reference driver), an empty `hyperv.Config.DataDir`
must default to something that survives a restart, since a lost ledger
directly reproduces the bug #222 exists to fix. `ResolveRelativePaths`
therefore defaults an empty `DataDir` to `.boxy-agent/hyperv` *before*
anchoring against `baseDir`, rather than leaving it empty (the pattern
`RelativePathResolver`'s contract otherwise uses, which bails out early on
an empty field) — this is required specifically so an installed agent
service (`agent service install --config ...`, which persists
`ProviderConfigsBaseDir`) and an interactive `boxy agent serve --config
...` resolve to the *same* ledger file, since both share the same
config-file directory as `baseDir` even though their process working
directories differ (a service's SCM-chosen cwd vs. an interactive shell's
cwd). `Driver.New` applies one more fallback — resolving against the
process's current working directory — only for the case where no config
file was ever supplied at all (no `baseDir` known), which is a real but
narrower gap: documented, not solved, here.

### Verified: the range-mode `host` survives the remote-agent transport unchanged

The design above rests on `Allocate → PersonalizeGuest` running inside this
driver — true locally, but the normal deployment is `boxy agent serve`
talking to the daemon over gRPC (ADR-0005), a separate layer this ADR does
not otherwise touch. Traced end to end: agent-side,
`pkg/agentsdk/remoteclient.go` calls this driver's `PersonalizeGuest`
directly and forwards `result.AccessDetails.Properties` — the `host` key,
range mode's ledger-sourced address included — verbatim into the wire
`PersonalizeGuestResult`. Daemon-side, `pkg/agentsdk/remote.go`'s
`RemoteAgent.PersonalizeGuest` reconstructs `GuestAccessDetails` directly
from that wire value with no re-derivation or independent host query.
`pkg/agentsdk/embedded.go`'s local-agent path calls the driver directly,
same as a test. No layer between this driver and the daemon second-guesses
`host` by querying the guest again, so the "trust the ledger, not a
read-back" decision above is not undermined one layer up.

## Non-goals / explicitly out of scope

- **#223** (a `NetworkRangeReporter` driver capability to validate/
  auto-discover the switch's actual NAT range instead of trusting the
  operator-declared range blindly) is a separate, later fast-follow. This
  ADR's range is Phase 1: operator-declared only.
- **#224** (overlay network fabric research — WireGuard-go, a
  gateway-VM-per-host candidate architecture) is unrelated research, not
  implicated by this design.
- **Multi-host-same-L2 coordination.** The ledger is agent-local (one
  Hyper-V host, one ledger file). If a second Hyper-V host ever shares the
  same L2 range, the ledger's location becomes a real decision
  (daemon-coordinated vs. agent-local) — explicitly not addressed here.
- No `providersdk`/`agentsdk` interface changes. `Allocate`/`Delete` are
  already the right hook points on the existing `Driver` contract.
- No CLI surface change, and no schema-gen edit for `network.range` itself:
  pool-level `CreateConfig` (`config:` under a pool spec) is emitted as a
  bare `{"type": "object"}` in `cmd/schema-gen/main.go`'s `buildPoolsSchema`
  — it has no per-provider schema `$ref` the way host-level provider
  `Config` does, so it was never schema-validated even for `static_ip`. The
  one schema edit this change makes is hand-updating
  `internal/config/schema/providers/hyperv.config.schema.json` for the new
  host-level `data_dir` field (that file is hand-maintained, not generated
  — the drift test in `cmd/schema-gen/main_test.go` only guards the
  top-level `boxy.schema.json`). No bundled-skill or `docs/api.md` edit is
  needed: neither currently documents `static_ip` or any other
  provider-`config:`-level field.

## Consequences

- `hyperv.NetworkConfig.StaticIP`'s JSON/YAML tag gains `omitempty` (it was
  previously always required and always emitted); existing pool configs
  using `static_ip` are unaffected functionally, only the tag's emission
  behavior on an empty value changes.
- `Driver.Delete`'s signature changes to a named return (`err error`) to
  support the release-on-nil-return `defer`. Its behavior for existing
  callers is unchanged except in one new case: if the VM teardown itself
  succeeds but `releaseAddress`'s own disk write then fails, `Delete` now
  returns that error instead of `nil` — a deliberate choice (a failed
  ledger release is a real problem worth surfacing, not swallowing), not
  an oversight, but a caller that previously treated any `Delete` success
  as final should be aware a successful teardown can now still surface an
  error through this one narrow path.
- A resource that never gets a ledger entry — e.g. `Create`'s post-`Start-VM`
  ledger write itself fails after the VM is already running, mirroring the
  existing `resolveCreatedVMID` failure comment's "leave it for the
  periodic `ResourceLister` sweep" precedent — surfaces as a `Create` error
  in that one case (unlike the ID-resolution failure, `Create` does return
  an error here, since unlike that case there's a clear actionable failure
  to report), but does not attempt to tear down the already-running VM.
  `PersonalizeGuest` finding no ledger entry for an id is not itself an
  error — it's treated the same as "no `boxy_net_static_ip` in Notes"
  today: no networking is applied, guest DHCP/pre-baked config is trusted.
- Two pools that happen to declare the *same* `RangeKey` with *different*
  `DefaultGateway` values are not mutually protected: `reserveAddress` only
  excludes its own entry's gateway from allocation, so a VM from pool B can
  be handed pool A's gateway address. This is the practical edge of "no
  cross-pool-consistency policy is needed" above — self-contained entries
  avoid needing to *reconcile* two pools' declared gateways, but do not
  prevent two pools sharing a range from stepping on each other's reserved
  addresses. Not addressed here; avoid overlapping `network.range` CIDRs
  across pools on the same host until this is revisited.

## Update (2026-08-25)

#223, the `NetworkRangeReporter` fast-follow named above, is implemented.
See [ADR-0013](0013-hyperv-network-range-reporter.md) for the design: a new
optional `providersdk` capability discovers a switch's real IPv4 range from
its host vNIC and validates `network.range` against it (containment, not
equality) inside `Driver.Create` — not at pool registration/reconcile time
as originally described, since that layer never decodes per-provider
`config.switch`/`network.range` values. The auto-populate stretch goal
this ADR also mentioned remains deferred; ADR-0013 explains why it's not
simply a "not done yet" gap (it would recreate #222's collision at the
`RangeKey` boundary if the discovered range ever changes across a host
reboot).

## Update (2026-08-26)

#235 fixed a destructive bug in `assignGuestIP`, the in-guest apply
mechanism this ADR's "trust the ledger, not a read-back" decision builds on
(shared with `static_ip` mode). The script removed the guest's existing
IPv4 address but never its existing default route; a preheated resource is
always personalized a second time on its first `Allocate` (not an edge
case), and on that second apply `New-NetIPAddress`'s own `-DefaultGateway`
rejected the reapply *after* the working address was already torn out,
leaving the guest on APIPA while `PersonalizeGuest` — per this ADR's own
design — kept trusting `applyRangeIP`'s return value as authoritative with
no independent check. The fix has two parts, both inside the same
PowerShell Direct script/session so no second guest connection is needed:
clear the interface's existing default route alongside its address before
reapplying (idempotency), and re-query the guest's own state immediately
after applying, throwing if it doesn't confirm the apply: the address must
be present in a usable state (`Preferred`/`Tentative`, not
`Duplicate`/`Invalid` — a bare presence check would pass on exactly the
conflict-detection failure this exists to catch), and when a gateway was
requested, the `0.0.0.0/0` route must exist too (closing the silent-success
gap for both halves of what the original bug broke). This does not weaken
"trust the ledger, not a `Get-VMNetworkAdapter` read-back" above — the new
check reads the guest's own configured state over the same lag-free VMBus
channel used to apply it, not the host-side adapter view ADR-0012 already
distrusts.
