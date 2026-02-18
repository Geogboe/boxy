# Boxy

Boxy is a resource pooling and sandbox orchestration tool. It pre-provisions pools of VMs, containers, and other resources, then assembles them into on-demand sandboxes for labs, training, pentesting, and development environments.

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

**Pool** — A named, homogeneous inventory of pre-provisioned resources. Declared in config. Each pool carries its own provisioning config (type, provider type, driver-interpreted config blob) and policy (preheat, recycle). The pool IS the spec — there is no separate "blueprint", "template", or "spec" entity.

**Sandbox** — A user-facing environment containing 1..N resources drawn from pools. When resources move from pool to sandbox, post-allocation hooks run to personalize them (set credentials, configure networking, etc.). Boxy returns connection info to the user; it is not a proxy.

**Provider** — An external system that provides resources (Docker, Hyper-V, etc.). Has a type and zero or more configured instances. Provider connection details (socket, host, certs) are owned by the agent, not the server.

**Driver** — Code that knows how to talk to a specific provider type. Interprets provider instance config and pool provisioning config. Lives in `pkg/providersdk/drivers/`. Drivers auto-discover their environment where possible (e.g., Docker checks for local socket, Hyper-V discovers via PowerShell).

**Agent** — The runtime entity that executes provider operations using drivers. Can be:
- **Embedded (local):** runs inside `boxy serve`, handles providers declared in the server config.
- **Remote (distributed):** runs on a separate host, connects to the server via gRPC over TLS, auto-discovers local providers.

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
- Same type, same provider type, config is a subset → cache hit
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

Two top-level sections: `providers` (what the embedded local agent handles) and `pools` (what should be running).

```yaml
providers:
  - type: docker
  - type: hyperv

pools:
  - name: win2022-base
    type: vm
    config:
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

  - name: kali-attackers
    type: container
    config:
      image: kalilinux/kali-rolling
      command: ["/bin/bash"]
    policy:
      preheat:
        min_ready: 3
        max_total: 8
```

**Key design decisions:**
- `providers:` section is purely the embedded local agent's provider list. Drivers auto-discover connection details.
- Pool `config:` is an opaque blob interpreted by the driver. Different providers expose different config options.
- Remote agents don't appear in server config — they register dynamically.
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

See [examples/](examples/) for complete Hyper-V and Docker pool configurations.

---

## State Store

**bbolt** (pure Go embedded K/V) for runtime state: resources, sandboxes, agent registrations. Config (providers, pools) is NOT stored in the database — it's read from `boxy.yaml` on startup.

---

## CLI Surface

```
boxy serve                     — start the daemon
boxy sandbox create [flags]    — request a sandbox
boxy sandbox list              — list sandboxes
boxy sandbox destroy <id>      — destroy a sandbox
boxy agent list                — list agents
boxy agent token create        — create registration token
boxy agent revoke <id>         — revoke an agent
```

Pools are config-driven — no `boxy pool create` command. Pool state is observable via the API but not mutated via CLI.

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

- **Sandbox composition:** Does a sandbox have a pre-defined "class" (pentest-lab = 1 Kali + 2 Windows targets), or is it always assembled ad-hoc at request time? Possibly both — sandbox classes in config, ad-hoc via API.
- **Pool routing without explicit provider:** If a pool says `type: container` without naming a specific provider instance, how does the server decide which agent handles it? Labels/placement rules? Round-robin among capable agents?
- **Config reloads:** Is restart required on config change, or should `boxy serve` watch for changes and reconcile? Restart is simpler; hot reload is nicer.
- **Subset matching for build cache:** Structural comparison of opaque config blobs to determine if one is a "subset" of another. What are the exact semantics? Is shallow key comparison sufficient, or do we need deep structural comparison?
- **Auth for CLI users:** Who is allowed to request a sandbox? Token-based? OIDC? Out of scope for now?

## Status

Early development. See [ROADMAP.md](ROADMAP.md) for design details on upcoming work.

## License

TODO
