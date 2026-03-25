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
│  REST API (CLI)    gRPC server (agents)  │
│       │                   │              │
│  ┌────▼───────────────────▼────────────┐ │
│  │  Core: Pool Manager, Sandbox Mgr    │ │
│  │  PolicyController (reconciler)      │ │
│  └─────────────────┬───────────────────┘ │
│                    │                     │
│  ┌─────────────────▼───────────────────┐ │
│  │       Embedded local agent          │ │
│  │    (Docker, Hyper-V drivers)        │ │
│  └─────────────────────────────────────┘ │
└──────────────────────────────────────────┘

         Remote host:
┌──────────────────────────────────────────┐
│              boxy agent                  │
│  gRPC → server  |  auto-discovered      │
│                  |  provider drivers     │
└──────────────────────────────────────────┘
```

---

## Core Domain Model

**Resource** — A runtime record of a provisioned instance (VM, container, share, network, etc.). Has an ID, type, state, provider handle, and properties. Resources are single-use: once allocated to a sandbox they are never returned to a pool (ADR-0002). A resource does not carry a "spec" or "profile" — it is simply evidence that provisioning succeeded.

**Pool** — A named, homogeneous inventory of pre-provisioned resources. Declared in config. Each pool carries its own provisioning config (`type` identifies the provider/driver, `config` is a driver-interpreted opaque blob) and policy (preheat, recycle). The pool IS the spec — there is no separate "blueprint", "template", or "spec" entity.

**Sandbox** — A user-facing environment containing 1..N resources drawn from pools. Sandbox classes are defined in separate `.sandbox.yaml` files and instantiated via CLI. When resources move from pool to sandbox, post-allocation hooks run to personalize them (set credentials, configure networking, etc.). Boxy returns connection info to the user; it is not a proxy.

**Provider** — An external system that provides resources (Docker, Hyper-V, Podman, VMware, etc.). Providers have a type that maps to a driver. Provider connection details (socket, host, certs) are owned by the agent, not the server. Drivers auto-discover their environment where possible.

**Driver** — Code that knows how to talk to a specific provider type. Interprets pool provisioning config. Lives in `pkg/providersdk/drivers/`. Drivers auto-discover their environment (e.g., Docker checks for local socket, Hyper-V discovers via PowerShell). A pool's `type` field maps directly to a driver (e.g., `type: docker` → Docker driver, `type: hyperv` → Hyper-V driver).

**Agent** — The runtime entity that executes provider operations using drivers. Can be:
- **Embedded (local):** runs inside `boxy serve`, handles providers declared in `server.providers`.
- **Remote (distributed):** runs on a separate host, connects to the server via gRPC over TLS, auto-discovers local providers. Declared in the `agents:` config section so the server knows what to expect.

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

**REST API** — for CLI-to-server communication. Standard HTTP REST.

**gRPC over TLS** — for agent-to-server communication. Bidirectional streaming: agent dials the server (NAT/firewall friendly), server pushes work down the stream.

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

### Sandbox Access Model

Boxy is not a proxy. When a sandbox is created, Boxy returns connection info for each resource:
- SSH host/port/key for Linux VMs
- RDP address for Windows VMs
- SMB path for file shares
- Container exec/attach details
- etc.

The user connects with their native client. Connection info is generated by post-allocation hooks.

---

## Configuration

### Server Config (`boxy.yaml`)

Three top-level sections: `server` (embedded agent and server settings), `agents` (remote agents the server expects), and `pools` (what should be running).

```yaml
server:
  listen: ":9090"
  providers: [docker, hyperv]

agents:
  - name: build-host
    providers: [docker]

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
- `agents:` declares remote agents the server should expect. If a declared agent is not connected, the server can warn/alert. Remote agents authenticate via the token bootstrap flow (see [ROADMAP.md](ROADMAP.md)).
- Pool `type` is the provider type (`docker`, `hyperv`, `podman`, `vmware`). It maps directly to a driver and determines routing. The abstract resource category (container, VM) is derived from the driver's capabilities.
- Pool `config:` is an opaque blob interpreted by the driver. Different providers expose different config options.
- Specs/blueprints are NOT a separate entity. The pool owns its provisioning config inline.
- Config is stateless and declarative. Runtime state (resources, sandboxes) lives in the state store (bbolt). Config is read on startup.

### Pool Policy Structure

```yaml
policy:
  preheat:
    min_ready: N       # target number of ready resources
    max_total: N       # hard cap
  recycle:
    max_age: "168h"    # destroy and replace unused resources older than this
```

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
boxy sandbox create -f pentest-lab.sandbox.yaml -n 10  # 10 instances for a class

# Quick one-off (sugar)
boxy sandbox create --pool kali
boxy sandbox create --pool kali:3 --pool ubuntu-targets:1
```

The file-based path is the primary, repeatable, version-controlled way. The `--pool` shorthand is sugar for quick testing — it constructs the same sandbox object internally.

See [examples/](examples/) for complete configurations.

---

## State Store

**bbolt** (pure Go embedded K/V) for runtime state: resources, sandboxes, agent registrations. Config (server, agents, pools) is NOT stored in the database — it's read from `boxy.yaml` on startup.

---

## CLI Surface

See [`docs/cli-wireframe.md`](docs/cli-wireframe.md) for the canonical CLI reference with flags and example output.

```
boxy init                               — create starter boxy.yaml in current directory
boxy serve                              — start the daemon (API server + reconcile loop)
boxy status                             — check server health and summary
boxy config validate                    — validate config file and exit
boxy sandbox create -f <file>           — create sandbox from a spec file
boxy sandbox list                       — list sandboxes
boxy sandbox get <id>                   — get sandbox details
boxy sandbox delete <id>                — delete a sandbox
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
  cli/                         — CLI command wiring (cobra)
  config/                      — config loading
  model/                       — core domain types
  pool/                        — pool manager + Provisioner interface
  sandbox/                     — sandbox manager
  store/                       — store interface + bbolt impl
  agent/                       — agent types and runner
pkg/
  hooks/                       — hook runner (public, self-contained)
  policycontroller/            — reconciler (public, self-contained)
  providersdk/                 — driver interface + capabilities (public API for driver authors)
    drivers/
      docker/
      hyperv/
      process/
  resourcepool/                — generic pool data structure (public utility)
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

Early development. See [ROADMAP.md](ROADMAP.md) for design details on upcoming work.

## License

TODO
