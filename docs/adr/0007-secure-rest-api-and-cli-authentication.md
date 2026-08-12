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
