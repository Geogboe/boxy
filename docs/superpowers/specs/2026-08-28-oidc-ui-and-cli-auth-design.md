# Design: OIDC authentication for the web UI and CLI

Status: Draft — pending owner review before implementation
Date: 2026-08-28
Tracks: #172

## Problem

`boxy serve`'s web dashboard (`internal/server/ui.go`, mounted via
`registerUIRoutes`) has **no authentication today, regardless of
`authRequired`** — `registerRoutes` mounts it directly on the mux with no
wrapper, and `principalFromRequest` falls back to an admin-equivalent
`Principal{Role: APIKeyRoleAdmin}` for any request with no session in
context. So whenever `--ui` is on, the dashboard is an unauthenticated,
admin-equivalent view of every sandbox/pool/resource/agent on the daemon.
This was fine when the UI was a local-only debug view; it stops being fine
once the UI is something an operator might expose to a team.

Separately, CLI/API auth today is entirely bearer API keys (ADR-0007):
created once via loopback-only bootstrap (first admin), or by an existing
admin calling `POST /api/v1/api-keys` on someone else's behalf. There is no
self-service path, and no way for a human to authenticate against an
existing company identity provider instead of being handed a long-lived
static key by an admin.

#172 asks for: a configurable custom OIDC provider, login/logout/profile in
the web UI, and using the profile UI to set up an API key.

## Goals

- Configure a custom OIDC provider (issuer, client ID/secret, redirect URL,
  scopes) via `boxy.yaml`/env — no hardcoded provider.
- Web UI: `/login`, `/logout`, and a profile page showing the resolved
  identity and role.
- Self-service: a logged-in human can mint their own short-lived "personal"
  API key from the profile page, without an admin acting on their behalf.
- CLI: an interactive OIDC login flow (`boxy login --oidc` or similar) that
  gets a time-bound personal key and stores it in the OS keyring the same
  way `boxy login` already does for a directly-issued key.
- Admins keep the existing endpoint for long-lived service/application
  keys, unaffected by any of the above.
- Role mapping from OIDC claims to Boxy's existing three roles
  (user/auditor/admin) is configurable, not hardcoded to one IdP's claim
  shape.

## Non-goals (v1)

- Multiple simultaneous OIDC providers.
- SCIM or any other user-provisioning sync.
- Fine-grained per-resource RBAC beyond the existing three roles — this is
  purely about *how a principal gets established*, not a new authorization
  model.
- Loopback-redirect CLI flow (see Decision 5) — tracked as a possible
  follow-up, not built in v1 unless the owner picks it over device-code.

## Decisions

### 1. UI auth posture

When `server.oidc` is configured: the web UI requires a valid session for
every route except `/login`, the OIDC callback route, and static assets.
Missing or invalid session redirects to `/login`.

When `server.oidc` is **not** configured: preserve exactly today's
behavior — UI stays open, admin-equivalent fallback, unchanged. This
mirrors the existing `ServerSpec.Secrets` pattern (`SecretSpec.Validate`'s
comment: "intentionally not defaulted so deployment posture is explicit")
rather than introducing a new implicit default.

**Residual gap, deliberately not closed by this design:** an operator who
sets `authRequired: true` for the API but never configures OIDC still gets
an open, unauthenticated UI — auth on the API and auth on the UI remain
independently configured. Closing that (e.g., disabling the UI entirely
unless OIDC is configured or `--insecure` is passed) is a real option but
changes behavior for any existing deployment relying on today's open UI
with API auth on; call it out to the owner rather than deciding it here.

Implementation note: whatever session middleware is added must not change
`principalFromRequest`'s existing unauthenticated-admin fallback used by
`NewTestMux`/`authRequired: false` — that fallback is load-bearing for a
large share of `internal/server`'s existing test suite, and is orthogonal
to OIDC.

### 2. Session mechanism: server-side record

A session is a server-side record (persisted in the store, analogous to
`model.APIKey`: an opaque cookie value + hash, not the raw value, stored
server-side), not a stateless signed cookie. Chosen over a JWT/stateless
cookie because it matches the existing API-key model (hash persisted,
revocable) and because `/logout` needs to actually invalidate the session,
not just clear a client-side cookie the server can't revoke.

Cookie: `HttpOnly`, `Secure` when serving TLS, `SameSite=Lax`. Default TTL
is a config value (`server.oidc.session_ttl`, suggested default `12h`);
expired sessions are swept the same way other daemon-owned expiring state
is (`internal/sandbox/deleter.go`'s reconciler is the existing pattern to
follow, ticked alongside the other 10s reconcile loops).

### 3. Role mapping: configurable claim mapping

```yaml
server:
  oidc:
    issuer: https://keycloak.example.invalid/realms/boxy
    client_id: boxy
    client_secret: ${BOXY_OIDC_CLIENT_SECRET}
    redirect_url: https://boxy.example.invalid/auth/callback
    role_claim: groups           # which OIDC claim to read
    role_mapping:                # claim value -> Boxy role
      boxy-admins: admin
      boxy-auditors: auditor
      boxy-users: user
    default_role: ""             # role when no mapped claim value matches;
                                  # empty = deny login (fail closed)
```

Chosen over "everyone gets `user`, admins listed separately by
email/subject" and "first OIDC login becomes admin": explicit,
auditable, versionable in the config file, and doesn't trust an IdP-side
label boxy hasn't reviewed. `default_role: ""` fails closed — a user whose
claims don't match anything is rejected rather than silently granted the
lowest role, since "some IdP config drifted" and "this person genuinely
has no boxy role" should not look the same as a working login.

### 4. API key kinds: `model.APIKey` needs `Kind` and `Subject`

`model.APIKey` today (`pkg/model/api_key.go`) has `ID`, `Hash`, `Role`,
`Name`, `CreatedAt`, `ExpiresAt`, `RevokedAt` — no notion of *who* or *what
kind* minted it. Add:

```go
type APIKeyKind string

const (
    APIKeyKindService  APIKeyKind = "service"  // admin-issued, existing behavior
    APIKeyKindPersonal APIKeyKind = "personal" // self-service, tied to an OIDC subject
)

type APIKey struct {
    // ... existing fields unchanged
    Kind    APIKeyKind `json:"kind,omitempty" yaml:"kind,omitempty"`       // empty == service, for backward compat with existing records
    Subject string     `json:"subject,omitempty" yaml:"subject,omitempty"` // OIDC subject; empty for service keys
}
```

- Self-service (profile page, CLI login) mints `Kind: personal`,
  `Subject: <oidc sub>`, capped by a max TTL
  (`server.oidc.personal_key_max_ttl`, suggested default `12h`), `Role`
  resolved from the *current session's* mapped role at mint time (not
  re-resolved later — a role change takes effect on the next login/mint,
  same as how a changed systemd unit only takes effect on next reload).
- Admin-created keys via the existing `POST /api/v1/api-keys` stay
  `Kind: service`, `Subject: ""` — completely unaffected.

### 5. `Sandbox.OwnerID` identity stability — flagged for owner decision

`Sandbox.OwnerID` is set from `principal.KeyID` today (ADR-0007). A
personal key is short-lived by design (12h default) and will be re-minted
regularly — if `OwnerID` stays `KeyID`, a user's sandboxes from yesterday's
key become invisible to them under `authorizeSandbox`'s
`sb.OwnerID == string(principal.KeyID)` check, even though the same human
is asking.

Two options, **not decided by this document**:

- **(a)** For personal keys, set `OwnerID` to a stable subject identity
  (e.g. `"oidc:<subject>"`) instead of the ephemeral `KeyID`; service keys
  keep `OwnerID = KeyID` exactly as today (no stable human identity exists
  to fall back to for those).
- **(b)** Leave `OwnerID = KeyID` unconditionally; accept that self-service
  key rotation makes a user's own past sandboxes admin/auditor-visible
  only, not visible to that user via their own new key.

(a) is recommended — this is a real, currently-shipped ownership field and
changing its semantics deserves explicit sign-off rather than a
judgment call buried in an implementation PR.

### 6. CLI OIDC flow — flagged for owner decision

Two standard shapes, with a real discriminator specific to Boxy's actual
deployment shape:

- **Loopback-redirect** (`gh auth login`-style): CLI spins up a local
  `127.0.0.1:<port>` HTTP server, opens the system browser to the IdP,
  captures the redirect. Requires a browser co-located with the CLI
  process on the same network-local host.
- **Device-code grant** (RFC 8628): CLI prints a URL + short code, user
  opens it on *any* device, CLI polls the token endpoint. Works headless —
  no local browser required — but needs the IdP to support the device
  grant (Keycloak does; some SaaS IdPs restrict it to specific client
  registrations).

Boxy agents commonly run on remote or headless Windows Hyper-V hosts
reached over SSH/RDP with no locally-reachable browser at `127.0.0.1` from
the CLI's own perspective — loopback-redirect breaks in exactly that shape.
**Recommendation: device-code as the primary/only v1 CLI flow.**
Loopback-redirect is left as a possible follow-up for interactive-desktop
use, not built now (see Non-goals).

### 7. Local integration testing: Keycloak in Docker Compose

New `examples/oidc-keycloak/` stack: `boxy serve` + Keycloak, Keycloak
preconfigured (realm export or startup script) with a `boxy` client and
three test users/groups mapped to `admin`/`auditor`/`user`, structured like
the existing `examples/compose-secrets/`. This gives both a real IdP to
integration-test the OIDC flow against locally, and a concrete deployment
example.

**Overlaps with #206** ("docs: expand on deployment examples" explicitly
lists "add oauth2-proxy using authentik, keycloak or something as
stubs?"). This spec's compose stack should satisfy that part of #206
directly — cross-reference rather than building two Keycloak-in-compose
examples independently.

## Consequences

- `model.APIKey` gains two new optional fields; existing persisted keys
  (empty `Kind`/`Subject`) must keep behaving as `service` keys — no
  migration needed if `Kind == ""` is treated as `service` throughout.
- A new store surface for sessions (create/get-by-hash/delete/sweep-expired)
  is needed, sized like the existing `APIKey` store methods.
- `internal/server` needs new routes (`/login`, `/auth/callback`,
  `/logout`, a profile page/partial) and session middleware wrapping
  `registerUIRoutes`'s mux only when `server.oidc` is configured.
- New dependency: an OIDC/OAuth2 client library. `golang.org/x/oauth2` +
  `github.com/coreos/go-oidc/v3` are the standard, well-maintained choice
  (matches the project's dependency philosophy — reputable, well
  documented, broadly used) and are the leading candidate; confirm at
  implementation time nothing better-maintained has emerged.
- `docs/cli-wireframe.md`, the generated `docs/api.md` route catalog, and
  the bundled `boxy-cli` skill all need updates per the existing CLI/API
  change checklists once the new routes/commands are real.
- ADR-worthy once implemented: this is a real security-boundary decision
  (ADR-0007's sibling), not just a feature — write the ADR alongside
  implementation, not deferred after.

## Open questions for the owner (before implementation starts)

1. **UI-open-when-unconfigured posture** (Decision 1) — acceptable as a
   standing gap, or should `authRequired: true` alone start requiring
   *something* for the UI even without OIDC configured?
2. **`Sandbox.OwnerID` identity stability** (Decision 5) — option (a)
   [stable subject for personal keys] vs (b) [always `KeyID`, accept the
   visibility gap].
3. **CLI flow** (Decision 6) — device-code as recommended, or is
   loopback-redirect (or both) wanted in v1 after all?

## Follow-ups (not in this design)

- Multiple simultaneous OIDC providers.
- Loopback-redirect CLI flow, if device-code alone proves insufficient.
- Fine-grained RBAC beyond the three existing roles.
