# ADR-0019: Configurable OIDC loopback callback port

- **Status:** Accepted
- **Date:** 2026-08-28

## Context

Some OIDC issuers require an exact redirect URI and do not implement dynamic
loopback-port matching. Boxy's browser login currently binds
`127.0.0.1:0`, which makes those issuers unusable for the CLI.

## Decision

Add `--oidc-loopback-port` to `boxy login`. The default value is `0`, retaining
dynamic port selection. A nonzero value is validated as a TCP port and is
bound only to `127.0.0.1:<port>`. The actual listener port is used to build the
redirect URI.

The flag applies only to `--oidc --web`. It does not change the default
device-code grant, PKCE/S256, public-client behavior, callback state checks, or
token exchange.

## Consequences

Operators can register an exact callback such as
`http://127.0.0.1:49152/callback` without weakening the safer dynamic default.
The selected port may already be occupied; the command reports the listener
error and does not silently fall back to another port.
