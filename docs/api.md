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
| POST | `/api/v1/api-keys` | admin | Create a service key for a user, auditor, or admin role; raw value is returned once. |
| GET | `/api/v1/api-keys` | admin | List service-key metadata without hashes, personal keys, or raw values. |
| DELETE | `/api/v1/api-keys/{id}` | admin | Revoke a service key; repeated revocation is idempotent. |
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
| POST | `/api/v1/resources/purge` | admin | Preview or explicitly force cleanup of unreferenced destroyed and stale destroying/error resources. |

### Sandboxes

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/sandboxes` | user/auditor/admin | List owned sandboxes for users; all for auditors/admins. |
| GET | `/api/v1/sandboxes/{id}` | user/auditor/admin | Inspect a sandbox, subject to user ownership. |
| POST | `/api/v1/sandboxes` | user/admin | Create an owned asynchronous sandbox request. |
| DELETE | `/api/v1/sandboxes/{id}` | user/admin | Request asynchronous deletion. |
| POST | `/api/v1/sandboxes/{id}/extend` | user/admin | Extend an owned sandbox expiry. |
| GET | `/api/v1/sandboxes/{id}/guest-credential` | user/admin | Fetch process-local guest credentials once; subsequent fetches return 410 Gone. |
| POST | `/api/v1/sandboxes/{id}/exec` | user/admin | Queue one durable command execution and return its execution ID; command, command_text, and script are mutually exclusive. |
| GET | `/api/v1/sandboxes/{id}/exec/{exec_id}` | user/admin | Read bounded output chunks and lifecycle status after an opaque cursor. |
| POST | `/api/v1/sandboxes/{id}/exec/{exec_id}/cancel` | user/admin | Cancel a pending or running execution. |

### Agents

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/api/v1/agent-tokens` | admin | Mint a single-use remote-agent registration token. |
| GET | `/api/v1/agent-tokens` | admin | List registration-token metadata. |
| DELETE | `/api/v1/agent-tokens/{id}` | admin | Revoke an unused registration token. |
| GET | `/api/v1/agents` | auditor/admin | List registered agents, connection state, heartbeat time, and capacity samples. |
| DELETE | `/api/v1/agents/{id}` | admin | Revoke an agent identity. |

### Diagnostics

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/diagnostics/logs` | admin | Query bounded, redacted control-plane and server-observed agent diagnostics. |

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

`POST /api/v1/sandboxes/{id}/exec` accepts exactly one opaque input: a non-empty `command` array, a non-empty `command_text` string, or a `script` object. Positional command arguments and script-file arguments remain separate fields; the server never reconstructs command text by quoting or splitting argv. A script has base64-encoded `content`, a lowercase SHA-256 `digest`, `interpreter` (`auto`, `powershell`, or `sh`), and optional `args` array. Script content is limited to 4 MiB and the server recomputes the digest before dispatch. The sandbox must be ready; multi-resource sandboxes require `resource_id`. The request returns `202 Accepted` with an `exec_id` and does not wait for the guest command.

`GET /api/v1/sandboxes/{id}/exec/{exec_id}` returns execution status plus bounded output chunks after the opaque `from` cursor. Each response includes a `next` cursor; resume with that cursor after a disconnect to avoid duplicate chunks. Cursors identify chunk boundaries and are not offsets into output bytes. Output is capped at 1 MiB per execution and chunked at 64 KiB. When the cap is reached, the response includes an explicit truncation marker and the terminal status remains inspectable. Terminal records are retained for 24 hours.

`POST /api/v1/sandboxes/{id}/exec/{exec_id}/cancel` requests cancellation. Only one execution may be active for a resource: a concurrent request receives `409 Conflict` with `error=resource_busy` and the active execution ID. Different resources may execute concurrently. Active executions use a server-owned context, so a client disconnect does not cancel them; a restart marks pending/running records `interrupted` and never replays them.

CLI execution follows the same durable API. Normal `boxy sandbox exec` attaches live output; `--detach` prints the execution ID and returns; `--attach <exec-id>` reconnects to an existing execution. `--events` emits lifecycle/chunk events, while `--buffered` waits for terminal status before writing captured output. Use exactly one of positional arguments, `--command`, `--stdin`, or `--script-file`; `--command` and `--stdin` are opaque input and are never split through argv quoting.

## Diagnostics logs

`GET /api/v1/diagnostics/logs` is administrator-only. It returns bounded, redacted control-plane and server-observed agent events as `{"events": [...], "next_cursor": "..."}`. Filter with `since`, `level`, `component`, `pool`, `agent`, `resource`, and `limit`; pass the opaque `cursor` returned by a prior response for the next page. Events are retained for up to seven days and 10 MiB, with 100 events per page by default and 1000 as the hard maximum. Credentials, signed URL queries, and secret-bearing fields are removed before events are persisted or rendered. Query access is recorded in a separate audit log.

## Compatibility

The API version is part of the URL. Additive response fields are compatible within `/api/v1`; breaking changes require a new API version.
