# ADR-0010: Ephemeral guest credential delivery

- Status: Accepted
- Date: 2026-08-15

## Context

Hyper-V guests need a bootstrap credential during allocation, but the
credential used for ongoing access must not be stored in VM notes, resource
properties, Boxy state, or remote-agent configuration. Remote agents also must
not require a copy of the pool bootstrap secret in their local environment.

## Decision

- Store one bootstrap credential per pool in the server-owned state store. The
  admin-only REST endpoint and `boxy pool set-guest-credential <pool> --value -`
  accept the value only through stdin. The legacy `env:` secret reference
  remains an embedded/local-development fallback.
- Resolve a remote bootstrap credential through an authenticated unary agent
  RPC. The server authorizes the request from the mTLS identity and the
  resource's recorded origin pool and owning agent; it does not trust claims
  supplied by the agent.
- Hyper-V rotates the bootstrap password to a fresh random password during
  allocation and verifies the new credential before returning it. Only safe
  access metadata is persisted on the resource.
- Represent the returned credential as an opaque `GuestCredential` envelope.
  Keep it in a process-local sandbox map and expose it through a one-time
  authenticated endpoint. The server intentionally loses unclaimed values on
  deletion or restart.
- Let callers save the envelope in their own OS keyring with
  `boxy sandbox create --save-guest-cred`, or pass it to exec through the
  environment/stdin. Exec relays the envelope unchanged to the owning driver;
  credentials are never accepted as plain command-line flag values.

## Consequences

This removes long-lived guest passwords from VM metadata and eliminates
agent-host bootstrap-secret provisioning for remote Hyper-V agents. It adds a
small RPC and process-local delivery state, and callers must save or consume a
credential when it is delivered because Boxy deliberately provides no replay
path. Other providers remain unaffected and can ignore the optional envelope.

Hyper-V VM creation still needs a non-sensitive guest OS/user note so the
driver can select its connection mechanism. The local `env:` fallback is kept
for existing single-host development workflows, but remote deployments should
use the server-owned pool credential.

