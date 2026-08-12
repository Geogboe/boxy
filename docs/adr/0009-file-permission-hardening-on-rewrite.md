# ADR-0009: File-permission hardening on rewrite

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

`os.WriteFile(path, data, mode)` only asks the OS to apply `mode` when the
call *creates* a new file (`open(2)`'s mode argument is ignored unless
`O_CREATE` actually creates the inode). On rewrite of a pre-existing file —
reinstall, upgrade, re-run, reconnect — the file silently keeps whatever
permissions it already had. A file that was ever world- or group-readable
(an older Boxy version with looser defaults, a misconfigured umask, a
manual edit) stays that way forever across every subsequent write, even
once the code writing it asks for `0600`.

This was first found and fixed for `internal/cli/service_config.go`'s
`writeYAMLFile` during the service-install feature (2026-08, #146-#150).
Issue #158 tracked the same gap in three more CLI files. A repeat sweep
during the 2026-08 secure-exec-streaming prerelease (#153/#154/#158, landed
in #162) found it *also* unfixed on files holding actual private-key
material, which #158's own text didn't enumerate.

## Decision

Every `os.WriteFile` call whose target path may already exist — which in
practice means every path that isn't a fresh temp file about to be renamed
into place — is followed by an explicit `os.Chmod(path, mode)` using the
same mode. This is mechanical and cheap; there is no reason to accept the
gap anywhere it applies.

### Files carrying this fix

| File | What it writes |
|---|---|
| `internal/cli/service_config.go` (`writeYAMLFile`) | Service install YAML config (original fix, Task 10) |
| `internal/cli/init.go` | `boxy.yaml` starter config |
| `internal/cli/sandbox_create.go` | Generated `.env` file for a created sandbox |
| `internal/skills/skills.go` | Bundled-skill version marker and source-pointer files |
| `pkg/pki/ca.go` (`EnsureCA`, `IssueServerCert`) | CA private key/cert, server private key/cert |
| `internal/cli/agent_serve.go` (`persistAgentCredentials`) | Agent's mTLS client private key/cert, CA cert — rewritten on *every* successful registration, not just the first (see `agentsdk.RemoteClientConfig.OnRegistered`'s doc comment), so the rewrite case is the common case here, not an edge case |
| `pkg/providersdk/providers/devfactory/driver.go` (`generateSSHKey`) | Reference-driver simulated SSH key |
| `pkg/providersdk/providers/devfactory/store.go` (`saveStore`) | Reference-driver JSON state file |

### The one file checked and found *not* affected

`pkg/store/disk.go`'s `persistLocked` writes to `<path>.tmp` and then
`os.Rename`s over the target. `os.Rename` replaces the target's inode
outright rather than reusing its permission bits — the destination file's
mode after a rename is whatever the tmp file's mode was at creation
(always correct, since the tmp path is always freshly created via
`O_CREATE`), regardless of what the previously-existing target's
permissions were. Any file using this write-tmp-then-rename pattern is
safe by construction and does not need the `os.Chmod` treatment.

## Consequences

- The fix is purely additive (`os.Chmod` calls, no behavior change on the
  happy path where permissions were already correct) and each site is
  covered by a test that pre-creates the target with loose permissions,
  triggers a rewrite, and asserts the mode afterward — see
  `internal/cli/init_test.go`, `pkg/pki/ca_test.go`
  (`TestEnsureCA_ReappliesPermissionsOnRewrite`,
  `TestIssueServerCert_ReappliesPermissionsOnRewrite`), and
  `internal/cli/agent_serve_test.go`
  (`TestPersistAgentCredentials_ReappliesPermissionsOnRewrite`). These tests
  skip on Windows (POSIX permission bits don't apply) and are verified in
  CI's `ubuntu-latest` job instead.
- Before adding any new `os.WriteFile` call for a path that isn't a fresh
  temp file, check whether it needs this same treatment.
- `pkg/providersdk/providers/devfactory` is a reference/testing driver, not
  part of the CLI's trusted-material surface, but it was fixed anyway for
  consistency — the fix is cheap and the pattern should not have known,
  intentionally-left exceptions.
