# Current Delivery Notes

A running log of recently-landed feature batches and their supporting decisions. AGENTS.md only points here — this is the full detail.

- Secure REST/CLI management (#154), sandbox command execution (#153), and
  the file-permission-on-rewrite sweep (#158) landed together in #162
  (2026-08). See [ADR-0007](adr/0007-secure-rest-api-and-cli-authentication.md),
  [ADR-0008](adr/0008-streaming-command-execution.md), and
  [ADR-0009](adr/0009-file-permission-hardening-on-rewrite.md) for the
  decisions and their dated change notes.
- REST handlers are currently hand-wired under `internal/server/api_*.go`.
  Keep the route catalog, generated `docs/api.md`, CLI wireframe, and bundled
  skill synchronized when adding routes or commands; use `task generate`.
- API keys are hashed in the daemon store and raw values belong only in the OS
  keyring. The first admin key is loopback-bootstrap-only; TLS uses the Boxy CA
  by default, `--ca-cert` for custom trust, and explicit insecure overrides —
  including for a bare (schemeless) `--server` address, which defaults to
  `https://`, not `http://` (ADR-0007).
- `os.WriteFile`'s mode argument is ignored on rewrite of a pre-existing
  file — any new write site handling sensitive or config material needs an
  explicit `os.Chmod` follow-up unless it uses disk.go's write-tmp-then-rename
  pattern. See ADR-0009 for the full file list and why rename is exempt.
- For feature work, use TDD red/green/blue: add a failing test, implement the
  smallest fix, then review/refactor with `gopls`; finish with `task test`,
  `task lint`, and documentation/drift checks.
- Do not run long-running integration, race, browser, smoke, or full CI suites
  until implementation and focused unit tests for the entire requested batch
  are complete. Red/green TDD runs are encouraged for small unit tests per
  feature; defer the expensive end-to-end gates until the final work is ready.
- Prefer the quiet `agent:*` Taskfile wrappers for verbose test, race, smoke,
  and CI commands. They capture successful output in `.tmp` and print only
  bounded diagnostics when a command fails.
- Guest credential delivery for Hyper-V pools (#188/#189) is implemented with
  server-owned bootstrap storage, mTLS-authorized agent resolution, allocation
  time password rotation, one-time sandbox delivery, and caller-supplied exec
  credentials. See [ADR-0010](adr/0010-guest-credential-delivery.md) and
  the design spec for the accepted restart/lost-delivery behavior.
- Post-0.1.66 batch (2026-09-09, PR #362, released as v0.1.67) landed
  per-operation agent timeouts + a stuck-provisioning watchdog (#333/#337 —
  see [ADR-0020](adr/0020-bounded-agent-operations-and-provisioning-watchdog.md)),
  pool quarantine-exhaustion visibility (#328), the `go-psrp`
  `AddCommand`/`AddArgument` fork (#244, see `pkg/psdirect/CLAUDE.md`),
  agent-facing UI verbiage renamed to "host" (#332), a Pools view remodel
  (#327, see `docs/ui-design-language.md`), unblocked user-role CLI login
  (#359), hidden-by-default deleted resources (#353), deferred allocation-time
  IP assignment (#358), and PSRP session reuse across `apply_network` +
  `rotate_credential` (#361, see AGENTS.md's Guest Credentials section).
  Filed follow-ups: #350 (per-sandbox timeout doesn't scale with resource
  count) and #363 (reconsider the pools UI's "unassigned" sentinel design —
  its literal name is now reserved at config validation as a narrow fix, not
  a redesign).
- Cross-host mesh peering (#224, Decision 2) landed 2026-09-16: `pkg/meshnet`
  (WireGuard-go device lifecycle), `providersdk.MeshPeerer` on `hyperv` and
  `docker`, full `agentproto`/`agentsdk` wiring, and sandbox-triggered
  full-pairwise mesh peering whenever a sandbox's resources actually span
  more than one host. See [ADR-0022](adr/0022-cross-host-mesh-peering.md).
  `#224` itself stays open — Decisions 3 (JIT native-protocol access) and 4
  (`AccessBroker` extension point) are still separate, unimplemented plans.
- PR #369 (2026-09-17) batched the above with several real-hardware bug
  fixes found validating both on wks01 (`GlobalMemoryStatusEx` memory-query
  fallback, `psdirect` HvSocket dial retry, the `checkTemplateNotAttached`
  guard's two false-positive rounds, guest credential rotation/static-IP
  moved onto `vmsdk.GuestExecScript`), plus #368 (coalesced reconcile
  wakeups, `pkg/diagnostics` fast-append, `pkg/psdirect.ExecScript`, CI
  draft-PR gating). Real-hardware validation confirmed `NetworkIsolator`
  end-to-end for the single-host case (see ADR-0021's 2026-09-16 entry);
  cross-host `MeshPeerer` remains unverified against live infrastructure —
  see ADR-0022's Open Risks, including a newly-found two-agents-one-host
  topology gap unrelated to mesh peering itself.
- Per-sandbox network isolation (#224, Decision 1) landed 2026-09-14 across
  Plans 1a/1b/1c. Every sandbox now gets its own provider-level network —
  a Docker bridge, or a Hyper-V Internal vSwitch plus NAT over a `/29` from
  `10.250.0.0/16` — created on its first resource claim and torn down with
  the sandbox. It is automatic and universal with no opt-out: an agent
  advertises which provider types it can actually isolate, and one that
  advertises none (devfactory) is skipped rather than failed. Hyper-V's
  `AttachToSegment` also assigns the guest's address from the segment's own
  block, which made the driver's pool-level `network` config
  (`static_ip`/`range`, ADR-0012) and its range-validation capability
  (ADR-0013) dead — both removed, their code archived under
  `.archive/pkg/hyperv/`. See
  [ADR-0021](adr/0021-network-isolation-driver-capability.md) for the
  design and, importantly, for what is **not** settled: `New-NetNat`'s
  possible one-instance-per-host limit is still unverified and would break
  the second sandbox on a Hyper-V host if real. Also carries a deliberate
  in-memory guest-credential retention in the Hyper-V driver (the rotated
  credential is unavailable to `AttachToSegment` otherwise) worth a second
  look, and adds a PSRP session to the allocation hot path, compounding
  #350.
