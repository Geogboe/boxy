# oidc-keycloak — local OIDC login testing against Keycloak

A real identity provider to test Boxy's OIDC web-UI login against locally
(see `docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md`),
and a concrete `server.oidc` config example. Requires a running Docker
daemon.

This also covers the deployment-example ask in #206 ("add oauth2-proxy
using authentik, keycloak or something as stubs") — it's the same
Keycloak-in-compose need, not a second thing to build.

## What's here

- `compose.yaml` — Keycloak (dev mode, realm imported on startup) + Boxy.
- `realm-export.json` — a `boxy` realm with a confidential `boxy` client and
  three demo users, one per group/role:

  | username        | password                    | group          | maps to Boxy role |
  |-----------------|------------------------------|----------------|--------------------|
  | `admin-demo`    | `boxy-example-dev-password`  | `boxy-admins`  | `admin`            |
  | `auditor-demo`  | `boxy-example-dev-password`  | `boxy-auditors`| `auditor`          |
  | `user-demo`     | `boxy-example-dev-password`  | `boxy-users`   | `user`             |

  The client secret (`boxy-example-dev-secret-not-for-production`) is
  committed deliberately — this is a throwaway local realm with no real
  data, not a credential worth protecting. Never reuse this pattern for a
  real deployment; see `docs/superpowers/specs/2026-08-28-oidc-ui-and-
  cli-auth-design.md`'s Decision 3 for why `server.oidc.client_secret`
  otherwise requires an `env:NAME` reference rather than a literal.
- `boxy.yaml` — the matching `server.oidc.*` config, referencing the
  secret via `env:BOXY_OIDC_CLIENT_SECRET`.

`realm-export.json` also registers a public `boxy-cli` client (device-code
grant enabled, plus `standardFlowEnabled` and a `http://127.0.0.1:*`
redirect URI for `boxy login --oidc --web`'s loopback-redirect flow) —
`boxy.yaml`'s `cli_client_id: boxy-cli` wires it up.

## The issuer-consistency problem this compose file solves

A browser (running on your host machine) and Boxy (running in a
container) both need to agree on Keycloak's issuer URL for OIDC's
signature/issuer checks to pass — but "localhost" means different things
to each of them. This is solved here, not worked around:

- Keycloak's `KC_HOSTNAME: "http://localhost:8081"` fixes the issuer,
  authorization endpoint, token endpoint, and JWKS URI it reports to this
  one canonical value, regardless of which network path a request arrived
  on.
- Boxy's container gets `extra_hosts: ["localhost:host-gateway"]`, so
  "localhost" *inside* that container resolves to the Docker host instead
  of the container itself — reaching Keycloak's published port the same
  way your browser does.

Both sides end up calling the literal same `http://localhost:8081`, so
there's no issuer mismatch and no `InsecureIssuerURLContext` override
needed. This exact configuration was verified live during development: a
full authorization-code login (real Keycloak login form, real RS256-signed
ID token, real signature/issuer/nonce verification, `groups` claim mapped
to the `admin` role) round-tripped successfully through `boxy serve`
running against this realm.

## Run it

```sh
docker compose up
```

Then open <http://localhost:9090/login> and either:

- log in with the bootstrapped local admin (run `docker compose logs boxy`
  for the one-time password, or `docker compose exec boxy boxy admin
  bootstrap-password --config /etc/boxy/boxy.yaml`), or
- click "Log in with single sign-on" and use one of the demo accounts
  above.

From the CLI instead, against the same running stack:

```sh
boxy login --server http://localhost:9090 --insecure --oidc
# or: boxy login --server http://localhost:9090 --insecure --oidc --web
```

using any of the demo accounts above. Either grant mints a self-service
personal API key and stores it in the OS keyring, same as a directly
supplied `--api-key`.

**Note on the `boxy` image**: `ghcr.io/geogboe/boxy` is not published yet
(GoReleaser doesn't build a container image today — a separate, pre-existing
gap, not specific to this example). Until it is, run Boxy directly against
the Keycloak container's published port instead of via the `boxy` compose
service:

```sh
docker compose up -d keycloak
export BOXY_OIDC_CLIENT_SECRET=boxy-example-dev-secret-not-for-production
go run ./cmd/boxy serve --config examples/oidc-keycloak/boxy.yaml --insecure --listen 127.0.0.1:9090
```

(run from the repo root). This is exactly how the live end-to-end
verification above was performed — the `extra_hosts` trick is unnecessary
here since Boxy isn't containerized, and "localhost" already means your
host machine.
