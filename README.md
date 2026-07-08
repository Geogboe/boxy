# Boxy

Boxy is a resource pooling and sandbox orchestration tool. It pre-provisions pools of VMs, containers, and other resources, then assembles them into on-demand sandboxes for labs, training, pentesting, and development environments.

## Install

Release installers are available for Windows PowerShell, Linux, and macOS. They download the newest published GitHub release, verify it against the published `checksums.txt`, and install it into a user-local bin directory.

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.ps1 | iex
```

Linux / macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/Geogboe/boxy/main/scripts/install.sh | sh
```

See [docs/install.md](docs/install.md) for supported platforms, version pinning, environment overrides, PATH behavior, and verification details.

## How It Works

Boxy keeps pools of generic, ready-to-use resources warm ahead of time. When a user requests a sandbox, resources are pulled from pools and personalized via hooks — credentials are set, networking is configured, and connection info is returned. The user connects with their native client (SSH, RDP, SMB, etc.). Boxy is not a proxy.

```
┌──────────────────────────────────────────┐
│               boxy serve                 │
│                                          │
│         REST API + web dashboard         │
│                    │                     │
│  ┌─────────────────▼───────────────────┐ │
│  │  Core: Pool Manager, Sandbox Mgr    │ │
│  │  PolicyController (reconciler)      │ │
│  └─────────────────┬───────────────────┘ │
│                    │                     │
│  ┌─────────────────▼───────────────────┐ │
│  │       Embedded local agent          │ │
│  │    (Docker, Hyper-V drivers)        │ │
│  └─────────────────────────────────────┘ │
└──────────────────────────────────────────┘
```

A remote agent mode (`boxy agent`, connecting to the server over gRPC from a
separate host) is planned but not yet implemented — see #37/#62. Today, the
embedded local agent inside `boxy serve` is the only agent.

---

## Core Domain Model

**Resource** — A runtime record of a provisioned instance (VM, container, share, network, etc.). Has an ID, type, state, provider handle, and properties. Resources are single-use: once allocated to a sandbox they are never returned to a pool (ADR-0002). Resources also retain immutable pool provenance so the daemon can reason about capacity even after allocation removes them from ready inventory.

**Pool** — A named, homogeneous inventory of pre-provisioned resources. Declared in config. Each pool carries its own provisioning config (`type` identifies the provider/driver, `config` is a driver-interpreted opaque blob) and policy (preheat, recycle). The pool IS the spec — there is no separate "blueprint", "template", or "spec" entity.

**Sandbox** — A user-facing environment containing 1..N resources drawn from pools. Sandbox creation is asynchronous on the server: the API persists a sandbox request in `pending`, the reconcile loop fulfills it, and the sandbox transitions to `ready` or `failed`. When resources move from pool to sandbox, post-allocation hooks run to personalize them (set credentials, configure networking, etc.). Boxy returns connection info to the user; it is not a proxy.

**Provider** — An external system that provides resources (Docker, Hyper-V, Podman, VMware, etc.). Providers have a type that maps to a driver. Provider connection details (socket, host, certs) are owned by the agent, not the server. Drivers auto-discover their environment where possible.

**Driver** — Code that knows how to talk to a specific provider type. Interprets pool provisioning config. Lives in `pkg/providersdk/drivers/`. Drivers auto-discover their environment (e.g., Docker checks for local socket, Hyper-V discovers via PowerShell). A pool's `type` field maps directly to a driver (e.g., `type: docker` → Docker driver, `type: hyperv` → Hyper-V driver). Docker pools automatically pull a missing image on first provision instead of requiring a manual `docker pull`.

**Agent** — The runtime entity that executes provider operations using drivers. Today this is only the **embedded (local)** form: it runs in-process inside `boxy serve` and handles providers declared in `server.providers` (see `pkg/agentsdk/`). A **remote (distributed)** agent — a separate `boxy agent` process connecting back to the server over gRPC — is planned but not yet built (#37/#62); there is no `agents:` config section today.

The agent is the execution layer — Boxy core delegates all provider IO through the agent, never directly to drivers. The `Provisioner` interface is the agent seam.

**PolicyController** — The reconciler. Runs on a tick inside `boxy serve`. Compares desired pool state (from policy) to actual state and triggers provisioning/destruction via the agent. Stateless and idempotent — every tick re-derives what's needed from scratch. One controller reconciles all pools.

**Hooks** — Side-effect notifications that run after events occur (resource provisioned, sandbox created, etc.). Not the control flow — hooks are for "resource provisioned -> send webhook" or "sandbox allocated -> set credentials", not for triggering provisioning itself. Lives in `pkg/hooks/`.

---

## Architecture

### Server, CLI, and Agent (Vault-like model)

Single binary (`boxy`), three modes:

```
boxy serve                     — daemon: pool reconciler, REST API, gRPC agent server
boxy <command>                 — CLI client: talks to daemon via REST
boxy agent                     — distributed agent: connects to server via gRPC
```

**REST API** — for CLI-to-server communication. Standard HTTP REST, always served alongside the web dashboard by `boxy serve` (no separate `api.enabled` gate). This is implemented today and is the primary interaction layer.

**gRPC over TLS (planned, #37/#62)** — for future agent-to-server communication. Bidirectional streaming: agent dials the server (NAT/firewall friendly), server pushes work down the stream. No gRPC transport exists in the codebase yet.

### Pool Routing

A pool's `type` field identifies the provider type (e.g., `docker`, `hyperv`, `podman`, `vmware`). The system routes work to any agent — embedded or remote — that has a matching provider. The abstract resource category (container, VM) is derived from the driver's capabilities, not declared on the pool.

If multiple agents support the same provider type, the system picks a capable agent. An optional `agent:` field on the pool can pin it to a specific agent when needed.

### Reconciliation Flow

```
serveLoop ticker
    └─ PolicyController.Reconcile(pool)
           observes: pool has 1 ready, policy says min_ready=3
           gap: need 2 more
           └─ pool.Manager.EnsureReady(pool, count=2)
                  └─ Provisioner.Provision(pool)  ← agent impl
                         └─ driver.CreateVM / CreateContainer
```

### Pool Build Cache (Cross-Pool Resource Reuse)

The provisioner can "steal" surplus resources from other pools when they share compatible config, instead of building from scratch. Compatibility is discovered automatically at runtime — no explicit `base:` references between pools.

**Matching rules:**
- Same type, config is a subset → cache hit
- Surplus only: steal from Pool X only if `X.ready > X.policy.preheat.min_ready`
- If a match is found, take the resource and apply the delta (install packages, configure, etc.)
- If no match, build from scratch

The config comparison is structural — Boxy core compares the opaque config blobs without understanding their contents. YAML anchors (`&`/`*`) can be used for DRY in the config file without creating Boxy-level coupling.

### Post-Allocation Hooks

When a resource moves from a pool into a sandbox, hooks run to personalize it:
- Set user credentials
- Configure hostname/networking
- Apply sandbox-specific policies

Resources in pools are intentionally generic (no specific user, no credentials). Hooks make them specific at allocation time. This means credentials don't exist until allocation — they are generated/set by the hook and returned as connection info.

### Async Sandbox API Flow

Sandbox creation is a server-side async workflow:

1. Client `POST`s a sandbox request to `/api/v1/sandboxes`
2. Server persists the sandbox and returns `202 Accepted` with `status: "pending"`
3. The daemon reconcile loop provisions and allocates resources
4. Client polls `GET /api/v1/sandboxes/{id}` until status becomes `ready` or `failed`

The create API uses resource requests rather than allocated resource IDs:

```json
{
  "name": "pentest-lab",
  "requests": [
    {"type": "container", "profile": "kali", "count": 3},
    {"type": "container", "profile": "ubuntu-targets", "count": 1}
  ]
}
```

### Sandbox Access Model

Boxy is not a proxy. When a sandbox reaches `ready`, Boxy returns connection info for each resource:
- SSH host/port/key for Linux VMs
- RDP address for Windows VMs
- SMB path for file shares
- Container exec/attach details
- etc.

The user connects with their native client. Connection info is generated by post-allocation hooks.

---

## Configuration

### Server Config (`boxy.yaml`)

Two top-level sections today: `server` (listen address, embedded agent providers, web UI toggle) and `pools` (what should be running). A future `agents:` section for remote agents is planned alongside #37/#62 but doesn't exist yet.

```yaml
server:
  listen: ":9090"
  providers: [docker, hyperv]

pools:
  - name: win2022-base
    type: hyperv
    config: &win2022
      template: "Windows Server 2022 Standard"
      generation: 2
      cpu: 4
      memory_mb: 8192
      disk_gb: 80
      network_switch: "LabSwitch"
    policy:
      preheat:
        min_ready: 5
        max_total: 10
      recycle:
        max_age: 168h

  - name: kali
    type: docker
    config:
      image: kalilinux/kali-rolling
      command: ["/bin/bash"]
    policy:
      preheat:
        min_ready: 3
        max_total: 8
```

**Key design decisions:**
- `server.providers` declares what the embedded local agent handles. Drivers auto-discover connection details (socket paths, PowerShell, etc.) — no connection config needed.
- Pool `type` is the provider type (`docker`, `hyperv`, `podman`, `vmware`). It maps directly to a driver and determines routing. The abstract resource category (container, VM) is derived from the driver's capabilities.
- Pool `config:` is an opaque blob interpreted by the driver. Different providers expose different config options.
- Specs/blueprints are NOT a separate entity. The pool owns its provisioning config inline.
- Config is stateless and declarative and is read once on startup. Runtime state (resources, sandboxes) lives in the state store — see [State Store](#state-store) below.

### Pool Policy Structure

```yaml
policy:
  preheat:
    min_ready: N       # target number of ready resources
    max_total: N       # hard cap across ready + allocated resources from this pool
  recycle:
    max_age: "168h"    # destroy and replace unused resources older than this
```

Implementation note: preheat/recycle planning logic is intentionally kept in
`internal/pool` (not exposed as a public `pkg/` API) because this policy is
Boxy-specific domain behavior rather than a generic reusable SDK contract.

### Sandbox Definitions (`.sandbox.yaml`)

Sandbox classes are defined in separate files, not in the server config. A sandbox definition specifies which pools to draw resources from and how many:

```yaml
# pentest-lab.sandbox.yaml
name: pentest-lab
resources:
  - pool: kali
    count: 3
  - pool: ubuntu-targets
    count: 1
```

Sandboxes are instantiated via CLI:

```
# From a file (primary path)
boxy sandbox create -f pentest-lab.sandbox.yaml

# Return after the daemon accepts the request instead of waiting for ready/failed
boxy sandbox create -f pentest-lab.sandbox.yaml --no-wait
```

The file-based path is the primary, repeatable, version-controlled way. The CLI compiles pool references from the spec into daemon API requests, submits them to `boxy serve`, and waits for a terminal sandbox status by default. If a matching pool has exhausted its `max_total` hard cap, the sandbox request fails with `status: "failed"` rather than provisioning beyond the cap.

See [examples/](examples/) for complete configurations.

---

## State Store

Runtime state (resources, sandboxes, pool inventory) is persisted via the `pkg/store.Store` interface. Today that's `DiskStore` — a plain JSON file (`.boxy/state.json` by default) — plus an in-memory implementation used in tests. A `bbolt`-backed implementation was the original plan and may still land, but isn't implemented; `DiskStore`'s own doc comment notes it exists specifically so the CLI works end-to-end without pulling in a new dependency until that's needed. Config (`boxy.yaml`) is NOT stored in the state store — it's read fresh on every `boxy serve` startup.

---

## CLI Surface

See [`docs/cli-wireframe.md`](docs/cli-wireframe.md) for the canonical CLI reference with flags and example output.

```
boxy init                               — create starter boxy.yaml in current directory
boxy serve                              — start the daemon (API server + reconcile loop)
boxy status                             — check server health and summary
boxy config validate                    — validate config file and exit
boxy sandbox create -f <file>           — create sandbox from a spec file (waits by default; use --no-wait to return after acceptance)
boxy sandbox list                       — list sandboxes
boxy sandbox get <id>                   — get sandbox details
boxy sandbox delete <id>                — delete a sandbox (waits by default; use --no-wait to return after acceptance)
boxy sandbox extend <id> <duration>     — push a sandbox's auto-destroy expiry further out
```

**Planned (not yet implemented):**

```
boxy agent list                         — list agents and connection status
boxy agent token create                 — create registration token
boxy agent revoke <id>                  — revoke an agent
```

Pools are config-driven — no `boxy pool create` command. Pool state is observable via the API and web dashboard.

---

## Project Layout

```
cmd/boxy/                      — entry point
internal/
  cli/                         — CLI command wiring (cobra) + HTTP API client helpers
  config/                      — config loading and validation
  pool/                        — pool manager + Provisioner interface
  sandbox/                     — sandbox manager, async deletion/expiry reconciler
  server/                      — HTTP API handlers + embedded web dashboard
  skills/                      — bundled coding-agent skill assets
pkg/
  agentsdk/                    — Agent interface + embedded (in-process) implementation
  model/                       — core domain types (Resource, Pool, Sandbox)
  policycontroller/            — reconciler (public, self-contained)
  providersdk/                 — driver interface + capabilities (public API for driver authors)
    providers/
      docker/
      hyperv/
      devfactory/               — in-memory reference driver used for tests/local dev
  resourcepool/                — generic pool data structure (public utility)
  store/                       — store interface + DiskStore (JSON) / in-memory impls
```

`internal/` = Boxy's private business logic.
`pkg/` = self-contained, no `internal/` dependencies. The compiler enforces this boundary.

---

## Open Questions

- **Config reloads:** Is restart required on config change, or should `boxy serve` watch for changes and reconcile? Restart is simpler; hot reload is nicer.
- **Subset matching for build cache:** Structural comparison of opaque config blobs to determine if one is a "subset" of another. What are the exact semantics? Is shallow key comparison sufficient, or do we need deep structural comparison?
- **Auth for CLI users:** Who is allowed to request a sandbox? Token-based? OIDC? Out of scope for now?
- **Multi-agent routing:** When multiple agents support the same provider type, how does the server choose? Round-robin? Load-based? Labels? For now, `agent:` pinning on the pool is the escape hatch.

## Status

Early development. See the GitHub issue tracker for design details on upcoming work — notably #37/#62 (remote agent + auth, not yet built) and #124 (an open design discussion on reframing "sandbox" toward a job/scheduler model for long-running and interactive workloads).

## License

TODO
