# ADR 0014: Release signing with keyless cosign, gated by a protected Environment

Status: Accepted

## Context

#55 ("Harden release workflow and GitHub Actions security") tracked a batch
of release-pipeline hardening. Most of it shipped in the 2026-07 pass — SHA
pinning for third-party Actions, CODEOWNERS on `.github/workflows/`,
gitleaks in CI, narrow `GITHUB_TOKEN` permissions per job, and installer
checksum verification. The remaining scope, confirmed by re-reading
`release.yml` and `.goreleaser.yml` directly rather than the issue text, was
that **no release signing existed at all**: no `signs:` block, no
signing step in the workflow, and AGENTS.md's own "GoReleaser Signing Notes"
section already flagged this explicitly as not implemented.

Three decisions had to be made, each with a real tradeoff, talked through
with the maintainer rather than assumed:

1. Signing mechanism.
2. Where an approval gate lives, if one is wanted at all.
3. Whether to also add artifact attestations alongside signing.

## Decision

### 1. Keyless cosign (Sigstore/Fulcio/Rekor), not a GPG dedicated subkey

`.goreleaser.yml` gained a `signs:` block:

```yaml
signs:
  - cmd: cosign
    signature: "${artifact}.sigstore.json"
    args:
      - "sign-blob"
      - "--bundle=${signature}"
      - "${artifact}"
      - "--yes"
    artifacts: checksum
```

This is GoReleaser's own documented keyless-cosign example (sourced from
`goreleaser.com/customization/sign/` at implementation time, not
hand-written from memory — GoReleaser's `signs:` config shells out to an
external `cmd`, confirmed against the project's actual JSON schema for the
pinned `tools/go.mod` GoReleaser version rather than assumed). The
`release.yml` `goreleaser` job gained a SHA-pinned
`sigstore/cosign-installer` step (to put a real `cosign` binary on the
runner — GoReleaser does not embed a signer) and `id-token: write` in its
`permissions:` block (required for the OIDC token cosign exchanges for a
short-lived Fulcio cert).

Rejected: a GPG dedicated subkey (what #55's issue text originally
proposed). Tradeoffs weighed:

- **Key custody.** GPG requires generating a keypair, storing the private
  subkey material as a `GPG_PRIVATE_KEY`/`GPG_PASSPHRASE` secret pair
  forever, and owning rotation/revocation if it ever leaks. Keyless cosign
  has no long-lived private key anywhere — nothing to generate, store, or
  rotate.
- **What gets attested.** GPG signing answers "did the holder of this key
  approve this artifact" — a statement about a human's intent, disconnected
  from *which* CI run produced the binary. Keyless cosign answers "did this
  exact repo, workflow, and commit produce this artifact" — build
  provenance bound directly into the certificate identity, which is the more
  precise property for this project's actual threat model (a compromised or
  tampered release pipeline).
- **Verification UX.** GPG needs `gpg --import` plus an out-of-band way for
  a user to trust "this is really the maintainer's key." Cosign's
  certificate-identity check against a known GitHub Actions workflow ref is
  self-contained — no separate public key to distribute or trust
  out-of-band.

A `cosign`-with-a-stored-key middle ground was also considered and rejected:
it keeps cosign's verification UX but reintroduces GPG's exact key-custody
problem for no benefit, since this pipeline runs entirely on GitHub Actions
(no air-gapped CI that would force it).

### 2. Gate the existing `goreleaser` job, not a separate downstream sign job

The `goreleaser` job runs under a `release-signing` GitHub Environment,
which requires a manual approval click before *the entire job* — build,
publish, and sign — proceeds, not just the signature step.

Two placements were weighed:

- **Chosen: gate the whole `goreleaser` job.** `signs:` lives inside
  `.goreleaser.yml`, so `task release:check` (schema validation) and `task
  release:snapshot` (full pipeline dry run) exercise the signing config
  locally, matching this repo's "test locally before pushing" culture
  (AGENTS.md's `feedback_test_ci_locally` lesson). Cost: every release's
  entire build+publish waits on the approval click, not just the signature.
  Accepted because the same person who approves the gate already gates
  every release today by merging the release-please PR — one additional
  click, not new friction layered on top of none.
- **Rejected: a separate downstream `sign` job, gated on its own.**
  Binaries/checksums/SBOMs would publish immediately and automatically like
  today, with only the signature delayed behind approval. That's a more
  surgical trust boundary (the gate protects only the trust-conferring
  action), but it means `.goreleaser.yml` never contains `signs:` at all —
  the signing config becomes untestable by `task release:check`/`snapshot`
  and can only be verified against a live release run. Rejected specifically
  because it trades local verifiability for a marginal reduction in what's
  gated, for a single-maintainer repo where that marginal reduction doesn't
  change who approves what.

**Operational requirement, not automatable from here:** the `release-signing`
environment does not exist by default. It must be created in the repo's
Settings → Environments with a required reviewer *before* this change is
merged, or the `goreleaser` job will hang indefinitely waiting for an
approval gate that was never configured. This does not affect Release
Please's own job, which is ungated and unaffected — Release Please still
opens/merges the release PR and creates the tag/GitHub Release exactly as it
does today; only the subsequent `goreleaser` job's start is gated.

### 3. Cosign signing only — no `actions/attest-build-provenance`

#55's acceptance criteria mentioned both "a dedicated key/subkey path" and
"artifact attestations." Both cosign signing and GitHub's native build
attestations deliver overlapping provenance guarantees for the same
artifact set (checksums.txt); shipping both would be duplicated trust
machinery maintained for one artifact with no added assurance over the
other. Cosign signing was chosen as primary since it was already the
mechanism decision; attestations were deliberately not added. Revisit only
with a concrete reason cosign's guarantee is insufficient — not by default
alongside it.

**Note on #55's original acceptance criterion:** "release signing uses a
dedicated key/subkey path documented in the repo" is being reinterpreted by
this decision, not silently redefined — keyless signing has no key/subkey
by design; the equivalent guarantee here is the certificate-identity check
documented in `docs/install.md`.

## Non-goals / explicitly out of scope

- **Installer-side automatic signature verification.** `scripts/install.sh`
  and `scripts/install.ps1` still verify only `checksums.txt`'s hash, not
  its signature. #55's own text called this "long term" scope; it's
  deliberately deferred and tracked separately as #231 so it doesn't
  evaporate. Requiring the `cosign` CLI on every installing machine is a
  real new dependency (unlike checksum verification, which needs no extra
  tool) and deserves its own scoping decision — whether it's a hard
  requirement or an optional best-effort check.
- **Adding `cosign` to the `tools/` module.** GoReleaser's `signs:` needs
  `cosign` present in CI only (via `sigstore/cosign-installer`); it is not
  needed to run `task release:check`/`release:snapshot` locally, both of
  which validate everything short of the actual signing call. Adding
  cosign's full CLI dependency closure to `tools/go.mod` for local
  smoke-testing was considered and rejected as scope creep — `tools/go.mod`
  is scoped to buf and GoReleaser today, and cosign's transitive dependency
  set (`go run -modfile=tools/go.mod github.com/sigstore/cosign/v2/cmd/cosign`)
  is not fully vendored there, only the slice GoReleaser itself pulls in.

## Consequences

- Verifying a release now requires the `cosign` CLI and one command
  (documented in `docs/install.md`), not `gpg --import` plus a separate
  trust decision about a maintainer's public key.
- Every release requires one manual approval click in the GitHub Actions UI
  before it publishes — a deliberate, accepted friction point, not an
  oversight.
- Signing failures are visible locally: `task release:snapshot` fails with a
  clear `cosign: executable file not found` error on a machine without
  `cosign` installed, which is the expected boundary — it's exactly the gap
  `sigstore/cosign-installer` fills in CI, not a config bug.
- The exact `--certificate-identity-regexp` value documented in
  `docs/install.md` should be reconfirmed against the first real signed
  release before being treated as final — it was sourced from Sigstore's
  documented GitHub Actions identity format, not fabricated, but has not yet
  been checked against an actual signed artifact from this repo's workflow
  as of this ADR's initial acceptance.
