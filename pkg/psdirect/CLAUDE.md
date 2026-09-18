See ../../AGENTS.md for this project's general guidance. This file adds detail specific to this package.

## PSRP Transport Dependency Fork (go-psrp / go-psrpcore)

- `pkg/psdirect` (PowerShell Direct/HvSocket exec) depends on
  `github.com/smnsjas/go-psrp` and `github.com/smnsjas/go-psrpcore`, two
  third-party modules this project does not own. As of 2026-08-28 (#242),
  `go.mod` `replace`s both to public forks under `github.com/Geogboe/` —
  **a deliberate decision, not a default response to every upstream bug.**
  It was made specifically because #242, #244, and #257 all independently
  traced into the same pair of hardcoded/unconfigurable spots in these two
  modules with no tagged release to bump to (`go list -m -versions` on the
  pinned `go-psrpcore` returns nothing — pseudo-versions only), making it a
  shared, recurring blocker rather than a one-off. Forking took on real
  ongoing maintenance burden (merging upstream fixes, owning the diff) that
  should not be repeated lightly for the next unrelated third-party
  dependency issue — evaluate each such case on its own terms.
- The fork commits are minimal and isolated per fix, based directly on the
  exact pinned commit/tag (not on either fork's own `main`, which has
  diverged with unrelated upstream work not yet vetted for boxy's use) —
  `go-psrpcore`'s `fix/242-configurable-idle-read-timeout` (merged to that
  fork's `main`, since `main` there exactly matched the pinned commit) adds
  `Adapter.SetIdleReadTimeout`, defaulting to the original hardcoded 30s so
  every other caller sees unchanged behavior; `go-psrp`'s
  `fix/242-disable-hvsocket-idle-cap` (kept off `main`, tagged
  `v0.2.1-boxy242` instead, since that fork's `main` had moved past the
  pinned `v0.2.0`) calls it with `0` (disabled) on the HvSocket backend, so
  the idle cap defers entirely to the caller's own `ctx` deadline instead
  of an unrelated fixed timeout. Neither fork commit is verifiable against
  a live guest from this host — validated via each module's own unit
  tests (new ones added alongside the fix) plus confirming the *pre-existing*
  failures in `go-psrp`'s `./client` and `./hvsock` packages are identical
  on the unmodified pinned tag, not introduced by the fork.
- If re-forking or updating either fork: keep the diff scoped to one issue
  at a time (don't bundle #244's `AddCommand`/`AddArgument` work into a
  timeout fix, for example — see this package's `psdirect.go`'s
  `escapeNativeArg` doc comment for that separate, still-open gap), base new
  work on the exact commit/tag boxy currently pins rather than either fork's
  own `main`, and update `go.mod`'s `replace` pseudo-version/tag together
  with a note here.
- **2026-09-08 (#244): `go-psrp`'s fork now also carries `v0.2.2-boxy244`**,
  adding `Client.ExecuteCommand`/`ExecuteCommandStream` — a client-level
  entry point onto `go-psrpcore`'s existing
  `runspace.Pool.CreatePipelineBuilder()` +
  `pipeline.Pipeline.AddCommand`/`AddArgument`, which already existed on the
  pinned `go-psrpcore` commit (`v0.0.0-20260828053523-50f4720fbe2b`,
  unchanged — no `go-psrpcore` fork work was needed for this issue). The tag
  is a single commit (`928fd31`) directly on top of the pinned
  `v0.2.1-boxy242`, refactoring `Execute`/`ExecuteStream`'s internals
  (`pipelinePrereqs`, `invokePipeline`, `acquireSemaphore`, `wireUpStream`,
  `collectResult`) so the new command-based path reuses them rather than
  duplicating the connect/semaphore/NTLM-retry/receive-loop wiring; the
  existing script-based `Execute`/`ExecuteStream` behavior is unchanged —
  confirmed via the fork's own `go test ./client/...`, all green except the
  same pre-existing `TestConfig_Validate`/`TestSaveLoadState` failures this
  Windows dev host already produces against the unmodified pinned tag (SSO
  detection and file-permission-mode assertions that don't hold on Windows —
  reproduced identically on `v0.2.1-boxy242` before trusting them as
  pre-existing), and `./hvsock`'s pre-existing no-live-guest timeouts noted
  above.

  This package now calls `ExecuteCommand`/`ExecuteCommandStream` with a
  **fixed** wrapper script (`execScript`/`execStreamScript` — containing no
  caller data at all, so there is nothing left for PowerShell's own parser
  to mis-tokenize) and delivers `cmd`/`args` as structured CLIXML argument
  objects bound to the script's own `$args`, instead of building
  `& 'cmd' 'arg1' ...` as parsed text. This closes #244 for real: `psQuote`
  (the PowerShell-parser-level single-quote escaping #238 added) is deleted
  entirely. `escapeNativeArg` is deliberately **kept** — see its updated doc
  comment and [ADR-0008](../../docs/adr/0008-streaming-command-execution.md)'s
  2026-09-08 entry for why: it patches Windows PowerShell 5.1's own native
  command-line reconstruction when `&` spawns an external process, a hazard
  that sits downstream of PSRP argument delivery and is unaffected by
  whether the argument values arrived as parsed text or as `$args`/
  `AddArgument` objects. `AddCommand`/`AddArgument` alone also doesn't
  recover `$LASTEXITCODE` out of a single-command pipeline (`go-psrpcore`'s
  `PowerShell` object has no `AddStatement` to chain a second command) —
  hence the wrapper-script shape rather than passing `cmd` itself as the
  PSRP command name.
