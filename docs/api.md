# Boxy REST API

This document is generated from `server.APIRouteCatalog()` by `go generate ./...`. Do not edit it manually.

## Transport and authentication

`boxy serve` serves REST over HTTPS by default using the Boxy private CA. Public or enterprise-trusted certificates use the system trust store. Use `--ca-cert <path>` for a Boxy or custom CA. `boxy login --insecure` skips HTTPS verification for explicit development use only; `boxy serve --insecure` disables REST and agent-gRPC TLS.

All `/api/v1/*` routes except loopback bootstrap require:

```http
Authorization: Bearer <api-key>
```

Missing or invalid credentials return `401 Unauthorized`. Valid credentials without the required role return `403 Forbidden`.

API-key roles:

- `user`: manage owned sandboxes only.
- `auditor`: read-only access to shared resources.
- `admin`: full operator access.

## Routes

### Health

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/healthz` | none | Return ok when the HTTP server is alive. |

### API keys

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/api/v1/api-keys/bootstrap` | loopback | Create the first administrator key once; raw value is returned once. |
| POST | `/api/v1/api-keys` | admin | Create a user, auditor, or admin key; raw value is returned once. |
| GET | `/api/v1/api-keys` | admin | List key metadata without hashes or raw values. |
| DELETE | `/api/v1/api-keys/{id}` | admin | Revoke an API key. |
| POST | `/api/v1/api-keys/oidc-exchange` | id_token | Exchange a verified OIDC ID token (from `boxy login --oidc`) for a self-service personal API key; raw value is returned once. |

### Pools

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/pools` | auditor/admin | List configured pools and ready inventory. |
| GET | `/api/v1/pools/{name}` | auditor/admin | Inspect one pool. |
| POST | `/api/v1/pools/{name}/drain` | admin | Drain unused ready inventory. |
| POST | `/api/v1/pools/{name}/fill` | admin | Reconcile a pool to its configured target. |
| POST | `/api/v1/pools/{name}/guest-credential` | admin | Set a pool's guest bootstrap credential from a request body; the raw value is never returned. |

### Resources

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/resources` | auditor/admin | List resources. |
| GET | `/api/v1/resources/{id}` | auditor/admin | Inspect one resource. |

### Sandboxes

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/sandboxes` | user/auditor/admin | List owned sandboxes for users; all for auditors/admins. |
| GET | `/api/v1/sandboxes/{id}` | user/auditor/admin | Inspect a sandbox, subject to user ownership. |
| POST | `/api/v1/sandboxes` | user/admin | Create an owned asynchronous sandbox request. |
| DELETE | `/api/v1/sandboxes/{id}` | user/admin | Request asynchronous deletion. |
| POST | `/api/v1/sandboxes/{id}/extend` | user/admin | Extend an owned sandbox expiry. |
| GET | `/api/v1/sandboxes/{id}/guest-credential` | user/admin | Fetch process-local guest credentials once; subsequent fetches return 410 Gone. |
| POST | `/api/v1/sandboxes/{id}/exec` | user/admin | Execute a one-shot command; use stream=true for NDJSON events. |

### Agents

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/api/v1/agent-tokens` | admin | Mint a single-use remote-agent registration token. |
| GET | `/api/v1/agent-tokens` | admin | List registration-token metadata. |
| DELETE | `/api/v1/agent-tokens/{id}` | admin | Revoke an unused registration token. |
| GET | `/api/v1/agents` | auditor/admin | List registered agents, connection state, heartbeat time, and capacity samples. |
| DELETE | `/api/v1/agents/{id}` | admin | Revoke an agent identity. |

## Agent list response

`GET /api/v1/agents` retains the original `id`, `name`, `providers`, and `available` fields and adds the following fields:

```json
{"id":"agent-1","name":"Lab Hypervisor","providers":["hyperv"],"available":true,"connected":true,"last_seen":"2026-08-21T14:30:00Z","availability":{"hyperv":{"memory_mb":4096}},"availability_at":"2026-08-21T14:30:00Z"}
```

`connected` indicates an active embedded or remote transport; `available` indicates eligibility for new provisioning and can be false while a remote transport remains connected. `last_seen` and `availability_at` are optional RFC3339 UTC server-receipt timestamps. A missing provider entry in `availability` means no capacity sample was available, not zero capacity. Disconnected remote agents remain listed; the embedded agent has no heartbeat or capacity sample.


## Sandbox create request

```json
{"name":"lab","policies":{"auto_destroy_after":"1h"},"requests":[{"type":"container","profile":"web","count":1}]}
```

Sandbox creation returns `202 Accepted` and is fulfilled asynchronously by the daemon. User-key sandboxes are owner-scoped; ownerless legacy sandboxes remain visible only to auditors and admins.

## Sandbox execution

`POST /api/v1/sandboxes/{id}/exec` accepts a JSON object with a non-empty `command` array and optional `resource_id` and `timeout` fields. The sandbox must be ready; multi-resource sandboxes require `resource_id`. The default response is bounded JSON. Add `?stream=true` for `application/x-ndjson`: each data event has `type`, `stream`, and base64 `data`; the terminal event has `type=complete`, `exit_code` in `attributes`, and any error.

Output is bounded (1 MiB total, 64 KiB per chunk) in both modes. In the buffered (default) mode, a command that exceeds the limit returns `413 Payload Too Large` — any output already collected is discarded, since the buffered response has nothing to send until the whole thing is ready. Retry with `stream=true` for output that may be large: it delivers each chunk to the client as soon as it's produced, so exceeding the limit mid-command only truncates the tail — every chunk already sent stays delivered — and surfaces as an `error` field on the terminal `complete` event rather than an HTTP status change (headers are already flushed by the time streaming starts). A context-deadline timeout returns `504 Gateway Timeout`; any other provider/agent failure (including an unsupported-streaming capability error) returns `500 Internal Server Error` in buffered mode, or the same terminal `complete`-event `error` in streaming mode.

## Compatibility

The API version is part of the URL. Additive response fields are compatible within `/api/v1`; breaking changes require a new API version.
