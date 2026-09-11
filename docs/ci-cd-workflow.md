# CI/CD Workflow Notes

Detailed operational knowledge for this repo's CI/CD: merging conventions, release cadence, GitHub Actions pinning, running Linux CI jobs locally on this Windows host, and GoReleaser's cosign signing setup. AGENTS.md only points here — this is the full detail.

## Merging PRs

- `main` has no branch protection (`gh api repos/Geogboe/boxy/branches/main/protection` → 404). Merges are gated by convention and green CI, not by GitHub-enforced required checks.
- History uses merge commits, not squash, for every PR including release-please PRs — use `gh pr merge --merge --body ""`. The trailing `--body ""` is load-bearing, not cosmetic — see the changelog-duplication note below.
- **Every regular fix/feat PR's changelog entry was appearing twice in release-please's notes until 2026-08-27 — always pass `--body ""`.** Root cause: the repo's merge-commit template (`merge_commit_message: "PR_TITLE"`) puts the PR title into the merge commit's *body*, and PR titles here are themselves Conventional-Commit-formatted (`fix(pool): ...`) — identical in shape to the underlying fix commit already in history. release-please's commit parser walks every commit including merge commits, so it picked up both as separate entries for the same change (see `v0.1.50`'s release notes for a live example: `#240`'s fix is listed twice, once per commit SHA). There is **no repo-settings fix** for this: GitHub only allows three `(merge_commit_title, merge_commit_message)` combinations — `(PR_TITLE, PR_BODY)`, `(PR_TITLE, BLANK)`, `(MERGE_MESSAGE, PR_TITLE)` — and every combination other than the current one either keeps the duplicate body or moves the same duplicate text into the merge commit's *subject* instead. The fix has to happen per-merge: `gh pr merge --merge --body ""` explicitly overrides the template with an empty body via the API, leaving the merge commit as subject-only (`Merge pull request #N from owner/branch`), which doesn't match Conventional-Commit shape and isn't picked up as its own entry. This only prevents *future* duplication — releases already cut (`v0.1.50` and earlier) keep their duplicated notes unless hand-edited separately.
- release-please PRs reliably show their `CI` check as `action_required` with zero jobs run (seen for 0.1.27 and 0.1.29). This is a known, harmless quirk of that workflow's trigger conditions, not a real gate — safe to merge through.
- **Batching several small, independent fixes into one local integration
  branch before opening any PR** (rather than one branch-and-PR per issue
  against a moving `main`) is a deliberate, requested pattern, not just a
  shortcut — see PR #264 (2026-08-27, six issues: #241/#251/#258/#249/#213/#104).
  Build each fix on its own short-lived topic branch off the integration
  branch as usual (keeps commit messages/`Closes #N` and, if plans change,
  the option to cherry-pick just one fix out later), `git merge` it into the
  integration branch immediately, and **run the full build/test/`task
  ci:validate` gate again after every single merge**, not just once at the
  end — this caught two separate real semantic-merge regressions in the
  same session (see the two entries above) that a single end-of-batch check
  would have found much later and made harder to bisect. If the integration
  branch already has open PRs from an earlier attempt at per-issue branches
  covering some of the same commits, close those as superseded (referencing
  the new batch PR) once the batch PR exists — don't leave both live.
- **One planning session per batch** (2026-09-03 decision). Plan one
  substantial issue in a focused session, then spend the rest of the batch on
  low-hanging issues that already have clear scope and acceptance criteria.
  Do not start multiple heavyweight planning sessions concurrently or pause
  every small fix for a new design session. Keep each implementation on its
  own topic branch, merge it locally into the shared `dev` integration branch,
  rerun the full validation gate after each merge, and open one aggregate PR
  from `dev` to `main` when the batch is substantial.
- **Push/PR/CI cadence is deliberately low, separate from the batching
  pattern above (2026-08-28 decision).** The mechanics above (topic branch
  per fix, merged into one local integration branch, full `task ci:validate`
  after every merge) are the unit of work; pushing that branch, opening a
  PR, and letting GitHub Actions run is a separate, coarser-grained step —
  and the point of doing all validation locally first is specifically to
  avoid needing that remote round-trip more than necessary. Default to
  working through as much of the backlog as is actually batch-shaped (see
  the per-issue scoping judgment used for #264: skip anything needing live
  Hyper-V/macOS validation this host can't do, skip anything design/ADR-
  shaped, skip anything with no real scope yet) on one local branch across
  a whole session or more, closing a real batch of issues, before pushing
  and opening a PR at all. Push once that batch is substantial, not after
  every few items — local `task ci:validate` is what actually needs to pass
  before every push either way, so batching doesn't lower validation rigor,
  it just moves the GitHub Actions run (and the release-please/GoReleaser
  cadence question — see "Release cadence" below) to fire less often.
- Merging a release-please PR triggers `release.yml` on push to `main`. The `release-please` job tags and completes quickly; the `goreleaser` job (5 platforms + SBOMs + checksums + cosign signing, ~3 min of actual runtime) then **pauses indefinitely on a `release-signing` GitHub Environment approval** (#55, 2026-08, ADR-0014) before it starts — it will not run to completion on its own. Go approve it in the Actions run's UI, then wait for the run to complete before treating the release as published. Don't mistake the pause for a stalled/failed run.
- **Release cadence is deliberately not "cut a release after every merged fix" (2026-08-27 decision).** The project stays prerelease (`prerelease: true` in both `release-please-config.json` and `.goreleaser.yml`) until the owner says otherwise, but a prerelease that ships after a single one-line bugfix is still a wasted release: a full 5-platform GoReleaser run (SBOMs, checksums, cosign signing, a manual approval click) for one commit's worth of change. release-please already supports this — it keeps exactly one open release PR that accumulates every commit landed on `main` since the last release, updating in place, until that PR is merged. The fix is workflow discipline, not tooling: **don't merge the release-please PR just because it appeared.** Let multiple fix/feat PRs land on `main` first so the pending release PR accumulates a real batch of changelog-worthy entries, and only merge it — cutting the actual tagged release — when there's enough substance to justify a release, or when the owner explicitly asks for one. This overrides the ship-it skill's Phase 8 default (which treats "approve and merge the release PR" as an automatic follow-on to a self-approved fix PR merge) — ask before merging a release-please PR rather than doing it on autopilot.

- **Early-stage release scope:** Boxy has no users yet. Until that changes, aim to pack as much compatible, tested, and documented work into each release as practical. Batch related features, smaller improvements, and bug fixes together, but do not lower validation rigor or pull in blocked/design-only work without an explicit decision.
- **Green CI is not the same gate as "no one has commented on this PR."**
  During the post-0.1.66 batch (2026-09-09, PR #362), the aggregate batch PR
  was described as ready to merge purely on `task ci:validate` passing —
  without ever checking for Copilot's automated review or the repo owner's
  own inline PR comments, both of which were already present and included a
  real, confirmed bug (see #363 and ADR-0020's neighbors). Before merging any
  PR — batch or otherwise — check `gh api repos/<owner>/<repo>/pulls/<n>/reviews`
  and `.../comments` (not just `gh pr view`, which misses bot reviews and
  inline comments) and classify every finding, human or bot. The bundled
  `ship-it` skill's Phase 7.5 now makes this an explicit, non-skippable gate.

## GitHub Actions Node 24 migration — done

All actions in `ci.yml` and `release.yml` are pinned to Node 24-compatible
versions as of 2026-07 (`release-please-action` v5.0.0, `goreleaser-action`
v7.2.3). The `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24` workaround has been removed —
it's no longer needed. See #100.

## Action pinning and updates

- Every third-party action in `ci.yml` and `release.yml` is pinned to a full
  commit SHA (not a mutable tag), per #55. The tag is kept as a trailing
  comment (`@<sha> # vX.Y.Z`) for readability.
- `.github/dependabot.yml` watches the `github-actions` ecosystem and opens
  PRs to bump these pins — don't hand-edit them without also checking whether
  Dependabot would have caught the same update.
- `.github/CODEOWNERS` requires owner review on any `.github/workflows/`
  change.
- A `betterleaks` job runs in `ci.yml` on every push/PR and scans the full Git
  history of the checked-out ref with pinned Betterleaks `v1.8.1`. `task
  secrets:scan` installs and runs the same pinned version locally. Do not
  replace it with directory mode because that would miss secrets that were
  committed and later removed. Both the CI job and `task secrets:scan`/`task
  pii:scan` pass `--log-opts HEAD` (2026-08, PR #209 follow-up): without it,
  Betterleaks' `git .` source walks every ref reachable in the local
  repository, and `fetch-depth: 0` on `actions/checkout` fetches *all* remote
  branches (`+refs/heads/*`), not just the one being tested — so an unrelated
  open branch elsewhere in the repo (e.g. a stray test IP literal on someone
  else's in-progress PR) could fail the PII job for a PR that never touched
  it, and the same is true locally for any developer with those branches
  fetched. `--log-opts HEAD` still walks the *full* history of the checked-out
  ref back to its initial commit — no coverage of what's actually being
  merged is lost — it only drops sibling branches this run isn't about. On
  Windows,
  `scripts/betterleaks-git.ps1` prepends a narrow Git shim for a native ARM64
  Git-for-Windows compatibility issue: Betterleaks maps Go's `os.DevNull` to
  `NUL` for `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM`, while the
  `clangarm64` Git build rejects `NUL` as a config-file path, including Git
  `2.55.0.windows.3`. Testing on an x64 `mingw64` host did not reproduce it.
  The shim clears only those invalid paths, keeps system config disabled, and
  delegates to the real Git executable; it does not bypass Betterleaks or
  weaken the history scan. Retest native `betterleaks git .` after any
  Betterleaks/Git upgrade before removing the shim. See the independent
  Windows ARM64 reproduction at
  https://github.com/Gentleman-Programming/gentle-ai/issues/2206.

- A separate `pii` job uses `.betterleaks-pii.toml` to scan the checked-out
  ref's full Git history for non-controlled email addresses, private IPs, non-example hostnames,
  usernames, and home-directory paths. `task pii:scan` runs it locally;
  `task pii:scan:stdin` checks proposed public issue, PR, or comment text;
  `task pii:authors` reports Git author identities separately and is
  informational rather than blocking. Run the repository scan and the stdin
  scan before publishing public text. The archived external PII-scanner skill
  is not part of this workflow.
- Test and documentation fixtures must use scanner-recognized placeholders for
  fake credentials, such as `${BOXY_TEST_PASSWORD}`, `${BOXY_TEST_TOKEN}`, or
  `${BOXY_TEST_API_KEY}`. For identity-shaped fixtures, use
  `boxy-test@example.invalid`, `boxy.example.test`, TEST-NET/documentation IP
  ranges, `boxy-test-user`, and `C:\Users\boxy-test-user` or
  `/home/boxy-test-user`. Do not use realistic-looking random strings or
  common password words such as `password`, `changeme`, `testpass`, or
  `foo`/`bar`; those can be valid credentials and should remain visible to
  Betterleaks. Historical secret or PII fixture findings may be recorded only
  as narrow fingerprint-only `.betterleaksignore` entries after review.
- **Don't name your own real local infrastructure in checked-in docs**,
  including working/progress notes — e.g. a real Hyper-V test workstation's
  hostname. `.betterleaks-pii.toml`'s `boxy-pii-hostname` rule only matches a
  `host:`/`server:`-style key paired with a dotted FQDN
  (`host: foo.example.com`); a bare, dot-less label used in prose (e.g.
  `` `hostlabel` ``) is structurally outside that pattern and will not be
  flagged, so this is not a case the scanner catches for you. Use a
  descriptive phrase instead — "the local Hyper-V test host" — the same way
  the rest of this file avoids naming this project's actual dev machines.
  Found and fixed 2026-09-06 (docs/superpowers/specs/2026-09-04-*.md had
  named the author's real test workstation).

## GoReleaser Signing Notes

- GoReleaser publishes `checksums.txt` alongside release binaries and SBOMs,
  and (as of 2026-08, #55) signs it with **keyless cosign**
  (Sigstore/Fulcio/Rekor via the `goreleaser` job's GitHub OIDC token), not a
  GPG subkey — chosen so there is no long-lived private key to generate,
  store, or rotate. `signs:` lives in `.goreleaser.yml`, so `task
  release:check` / `task release:snapshot` exercise the config locally
  (signing itself still fails locally with "cosign: executable file not
  found" unless `cosign` is installed — that's expected; the real binary
  only needs to exist in CI, via the `sigstore/cosign-installer` step in
  `release.yml`). See [ADR-0014](adr/0014-release-signing-with-keyless-cosign.md).
- The `goreleaser` job runs under the `release-signing` GitHub Environment,
  which requires a manual approval click before the *entire* job (not just
  the signature) proceeds — chosen over gating a separate downstream signing
  job specifically so `signs:` could stay locally testable; see ADR-0014 for
  the tradeoff. This environment must be created in repo Settings before a
  release can complete — it does not exist by default, and the job will hang
  waiting for an approval gate that was never configured otherwise.
- Artifact attestations (`actions/attest-build-provenance`) were considered
  and deliberately **not** added alongside cosign signing — both deliver
  overlapping provenance guarantees for the same artifacts, and shipping
  both would be duplicated trust machinery for no added assurance.
  Reconsider only with a concrete reason attestations add something cosign
  doesn't, not by default.
- Installer-side automatic signature verification is still **not
  implemented** — tracked separately as #231, deliberately deferred out of
  #55 (that issue's own text called it "long term" scope). Don't assume
  `scripts/install.sh`/`scripts/install.ps1` verify anything beyond the
  checksum; verify against the actual scripts before claiming otherwise.

## Windows/WSL GitHub Actions validation

- This Windows ARM64 checkout can run Linux GitHub Actions jobs locally with
  `act` and Docker. Install `act` into the WSL user-local path; sudo is not
  required:

  ```powershell
  wsl.exe --cd D:\projects\code\boxy bash -lc "mkdir -p ~/.local/bin"
  wsl.exe --cd D:\projects\code\boxy bash -lc "curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/nektos/act/master/install.sh | sh -s -- -b ~/.local/bin"
  ```

- List or run Linux CI jobs from the Windows checkout with the ARM64 runner
  image:

  ```powershell
  wsl.exe --cd D:\projects\code\boxy bash -lc "~/.local/bin/act -l -W .github/workflows/ci.yml"
  wsl.exe --cd D:\projects\code\boxy bash -lc "~/.local/bin/act pull_request -j lint -W .github/workflows/ci.yml -P ubuntu-latest=catthehacker/ubuntu:act-latest --container-architecture linux/arm64"
  ```

- Use targeted Linux jobs such as `lint`, `build`, or `installer-smoke`.
  `act` does not reproduce GitHub-hosted Windows runners or release
  permissions; the repository test suite, `task ci:validate`, and GitHub
  Actions remain authoritative. Docker Engine must be available to WSL.
