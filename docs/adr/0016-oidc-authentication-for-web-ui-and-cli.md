# ADR-0016: OIDC authentication for the web UI and CLI

- **Status:** Accepted
- **Date:** 2026-08-28

## Context

`boxy serve`'s web dashboard had no authentication at all, regardless of
`authRequired`: `registerUIRoutes` was mounted with no wrapper, so `--ui`
exposed an admin-equivalent view of every sandbox/pool/resource/agent on the
daemon to anyone who could reach the port. Separately, CLI/API auth
(ADR-0007) was entirely bearer API keys minted by an existing admin — there
was no self-service path, and no way for a human to authenticate against a
company identity provider instead of being handed a long-lived static key.

#172 asked for a configurable OIDC provider, login/logout/profile in the
web UI, and self-service API key issuance from that profile. The full
design tradeoffs are recorded in
`docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md`; this ADR
records what was actually decided and built.

## Decisions

- **The web UI always requires a session**, independent of the REST API's
  own `authRequired`/OIDC configuration. There is no "open dashboard" mode.
  A bootstrapped local admin account (random/fixed username, random
  password, only the hash persisted, raw value shown exactly once via
  `boxy admin bootstrap-password`) covers login when no OIDC provider is
  configured — the same idiom as Jenkins' `initialAdminPassword` or
  Rancher's generated bootstrap password. This closes the gap a
  configuration-gated posture would leave: UI security is never a second,
  easily-forgotten toggle.
- **Sessions are server-side records** (`model.Session`: opaque cookie
  value, only its hash persisted), not stateless signed cookies — this
  matches the existing `model.APIKey` shape and lets `/logout` actually
  revoke a session rather than merely clearing a client-side cookie the
  server can't invalidate. `HttpOnly`, `Secure` when serving TLS,
  `SameSite=Lax`; expired sessions are swept by a `SessionSweeper`
  reconciler on the daemon's existing periodic tick.
- **Role mapping from OIDC claims is configurable**, not hardcoded to one
  IdP's claim shape: `server.oidc.role_claim` names the claim,
  `role_mapping` maps its values to Boxy's existing `user`/`auditor`/`admin`
  roles, and `default_role: ""` fails closed — an unmapped claim value is
  rejected rather than silently granted the lowest role, so "IdP config
  drifted" and "this person has no boxy role" don't look identical.
- **`model.APIKey` gained `Kind` (`service` | `personal`) and `Subject`.**
  Admin-issued keys via `POST /api/v1/api-keys` are unaffected
  (`Kind: service`, `Subject: ""`, empty `Kind` on existing persisted keys
  still means `service`). A `personal` key is self-service-minted, capped
  by a max TTL, with `Role` resolved from the *minting session's* mapped
  role (a later role change takes effect on the next login/mint, not
  retroactively).
- **`Sandbox.OwnerID` uses a stable identity for personal keys.** A
  personal key is short-lived by design and re-minted often; if `OwnerID`
  stayed the ephemeral `KeyID`, a user's sandboxes from yesterday's key
  would become invisible under `authorizeSandbox`'s ownership check even
  though the same human is asking. Personal keys get
  `OwnerID = "oidc:<subject>"` (OIDC) or `"local:<username>"` (the
  bootstrapped local-admin account minting its own personal key from the
  profile page — there is no OIDC subject to borrow, so it gets the
  analogous namespaced shape instead). Service keys keep `OwnerID = KeyID`
  exactly as before; `Principal.OwnerIdentity()` resolves the right one per
  principal kind.
- **CLI OIDC login supports two grants**, sharing the same
  token-exchange/session-minting backend: `boxy login --oidc` defaults to
  RFC 8628 device-code (works headless — no browser co-located with the
  CLI process, the common case for a boxy agent on a remote/headless
  Windows Hyper-V host reached over SSH/RDP); `--web` opts into a
  `gh auth login`-style loopback-redirect (`127.0.0.1:<port>` HTTP server,
  PKCE/S256 since the CLI is a public client with no secret) and
  auto-launches the system browser via a small per-OS `openBrowser` helper
  (`rundll32`/`open`/`xdg-open`) kept internal rather than pulled in as a
  dependency — Go's stdlib has no "open URL" primitive and this is too
  small to justify one.
- **Self-service personal-key minting exists in two places sharing one
  code path** (`Server.mintPersonalAPIKey`): the CLI's device-code/loopback
  exchange (`POST /api/v1/api-keys/oidc-exchange`, proven by a verified ID
  token, deliberately unauthenticated by any existing bearer key — this
  endpoint *is* the credential-issuance step) and the web UI's profile page
  (`POST /ui/profile/personal-key`, proven by an existing browser session).
  Both mint the same `Kind: personal` key shape; only the proof of identity
  differs.
- **New dependency:** `golang.org/x/oauth2` + `github.com/coreos/go-oidc/v3`
  — the standard, well-maintained choice for both the server's callback
  verification and the CLI's device-code/loopback grants.
- **Local integration testing:** `examples/oidc-keycloak/` — `boxy serve` +
  Keycloak in Docker Compose, preconfigured with a `boxy` client and three
  test users/groups mapped to `admin`/`auditor`/`user`. Also serves as a
  concrete deployment example (overlaps with #206's stub request).

## Consequences

- `internal/server` gained new routes (`/login`, `/logout`, `/auth/login`,
  `/auth/callback`, `/auth/cli-config`, `/ui/profile`,
  `/ui/profile/personal-key`) and `/api/v1/api-keys/oidc-exchange`, plus
  session middleware wrapping every UI route unconditionally.
- A local-admin account record (username + bcrypt password hash) is
  persisted like an `APIKey`, provisioned once on first `boxy serve`
  startup against a given state directory; `boxy admin
  bootstrap-password` retrieves the raw password exactly once.
- `docs/cli-wireframe.md`, the generated `docs/api.md`, and the bundled
  `boxy-cli` skill were updated for the new routes/flags per ADR-0007's
  existing CLI/API change checklist.
- No CSRF token was added for the new state-changing POST routes
  (`/login`, `/logout`, `/ui/profile/personal-key`): all three are
  same-origin form submissions relying on the session cookie's
  `SameSite=Lax`, which browsers don't attach to a cross-site POST, so a
  forged cross-site request arrives with no session cookie at all. Revisit
  if a future UI action needs to read the session cookie to authorize
  something with real consequences via a mechanism `SameSite=Lax` doesn't
  cover (e.g. a `<img>`/simple-form-shaped cross-site GET).
- Multiple simultaneous OIDC providers, SCIM/user-provisioning sync, and
  RBAC finer than the three existing roles remain explicitly out of scope.
