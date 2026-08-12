# ADR-0007: Secure REST API and CLI authentication

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

Boxy already exposes a REST API for pools, resources, sandboxes, and agents, but
its original daemon wiring assumed a trusted local machine. The next prerelease
must support remote CLI management without putting operator credentials in
configuration or runtime state.

## Decisions

- REST remains the primary CLI-to-daemon interaction layer under `/api/v1/`.
- The daemon serves REST over TLS by default, reusing the Boxy private CA and
  certificate machinery. `boxy serve --insecure` is an explicit local-development
  mode that disables REST and agent-gRPC transport TLS.
- CLI clients trust public/enterprise certificates through the system trust
  store. `--ca-cert` supplies a Boxy or other custom CA. Client `--insecure`
  keeps HTTPS but skips certificate verification and is never the default.
- A schemeless `--server host:port` address defaults to `https://`,
  matching the daemon's TLS-by-default posture — this is the single most
  natural way to point the CLI at a remote server, so it must resolve the
  same way the (secure, default) empty-address case does. Only an explicit
  `http://` prefix opts into plain HTTP, which only makes sense paired with
  `boxy serve --insecure`. `internal/cli/api_client.go`'s `apiBaseURL`
  (builds the connection) and `internal/credentials/credentials.go`'s
  `normalizeServerURL` (builds the keyring lookup key for the same address)
  must default identically — if they diverge, `boxy login --server
  host:port` and a later command's `--server host:port` compute different
  keyring keys and the stored credential appears to silently vanish.
- Operator credentials are bearer API keys. Only a SHA-256 hash and metadata are
  persisted; the raw value is returned once at creation and is never logged or
  written to Boxy config/state files.
- API keys have `user`, `auditor`, and `admin` roles. Authorization is enforced
  server-side; the CLI command tree is not role-specific.
- The first administrator key is created exactly once through a loopback-only
  bootstrap endpoint and is then stored by `boxy login` in the operating-system
  keyring using `github.com/zalando/go-keyring`. Logout removes both the key and
  any stored custom CA for that server.
- Sandbox ownership is recorded by API-key identity. Users can only see and
  mutate their own sandboxes; auditors are read-only across shared state; admins
  have full operator access. Legacy ownerless sandboxes remain visible to
  auditors/admins but not user keys.
- The API reference is generated from a checked-in route catalog with
  `go:generate` and committed under `docs/api.md`. A generator test must fail if
  the committed reference is stale.

## Consequences

- The disk and memory stores gain API-key records while retaining the existing
  JSON persistence backend.
- Existing local test muxes remain available without auth; the production daemon
  opts into authenticated TLS explicitly.
- Bootstrap and keyring behavior need platform-aware tests, while API policy
  tests should use injected stores and HTTP test muxes.
- API additions must update the route catalog, generated API reference,
  `docs/cli-wireframe.md`, and the bundled `boxy-cli` skill when they change the
  user-facing CLI.

## Change notes

- **2026-08-12**: A GitHub Copilot review on the PR implementing this ADR
  (#162) caught that the bare-address decision above wasn't actually
  implemented that way: `apiBaseURL` and `normalizeServerURL` both defaulted
  a schemeless `--server` address to `http://`, contradicting the
  TLS-by-default posture this ADR establishes — the single most natural way
  to point the CLI at a remote host (`--server myhost:9090`, no scheme)
  silently built a plaintext request against what's actually a TLS-only
  listener by default, and just failed to connect. Fixed by defaulting bare
  addresses to `https://` in both functions; two `internal/cli/status_test.go`
  cases that pointed a bare address at a plain-HTTP `httptest.Server` were
  updated to use the server's `srv.URL` (which already carries the correct
  explicit `http://`) instead, matching every other test in the package.
