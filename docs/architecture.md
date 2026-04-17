# Boxy — Architecture Overview

> **Last updated:** 2026-03-15
>
> Boxy is a resource pooling and sandbox orchestration tool. It pre-provisions
> pools of VMs, containers, and other resources, then assembles them into
> on-demand sandboxes for labs, training, pentesting, and development
> environments.

---

## Table of Contents

- [System Overview](#system-overview)
- [Runtime Modes](#runtime-modes)
- [Domain Model](#domain-model)
- [Package Architecture](#package-architecture)
- [Key Abstractions](#key-abstractions)
  - [Policy Controller (Observe → Decide → Act)](#policy-controller)
  - [Provider SDK & Driver Model](#provider-sdk--driver-model)
  - [Agent Layer](#agent-layer)
  - [Store Layer](#store-layer)
- [Reconciliation Flow](#reconciliation-flow)
- [Sandbox Allocation Flow](#sandbox-allocation-flow)
- [Data Flow](#data-flow)
- [Configuration Model](#configuration-model)
- [Technology Stack](#technology-stack)

---

## System Overview

Boxy follows a **Vault-like single-binary architecture** — one binary (`boxy`)
operates in three modes: daemon server, CLI client, and distributed agent.

```
┌──────────────────────────────────────────────────────────────────┐
│                        boxy serve (daemon)                       │
│                                                                  │
│   ┌─────────────┐   ┌────────────────┐   ┌──────────────────┐  │
│   │  REST API    │   │  gRPC Server   │   │ PolicyController │  │
│   │  (CLI cmds)  │   │  (agents)      │   │ (reconcile loop) │  │
│   └──────┬───────┘   └───────┬────────┘   └────────┬─────────┘  │
│          │                   │                     │             │
│   ┌──────▼───────────────────▼─────────────────────▼──────────┐ │
│   │              Core: Pool Manager + Sandbox Manager          │ │
│   │                          │                                 │ │
│   │              ┌───────────▼────────────┐                    │ │
│   │              │     Store (bbolt)      │                    │ │
│   │              └────────────────────────┘                    │ │
│   └───────────────────────────┬────────────────────────────────┘ │
│                               │                                  │
│   ┌───────────────────────────▼────────────────────────────────┐ │
│   │            Embedded Agent (local providers)                 │ │
│   │     ┌──────────┐  ┌──────────┐  ┌──────────┐              │ │
│   │     │  Docker   │  │ Hyper-V  │  │ DevFctry │  ...drivers  │ │
│   │     └──────────┘  └──────────┘  └──────────┘              │ │
│   └────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘

         Remote hosts:
┌──────────────────────────────────┐  ┌──────────────────────────┐
│      boxy agent (host A)         │  │   boxy agent (host B)    │
│  gRPC ──► server                 │  │  gRPC ──► server         │
│  auto-discovered drivers         │  │  auto-discovered drivers │
└──────────────────────────────────┘  └──────────────────────────┘
```

---

## Runtime Modes

```mermaid
graph LR
    Binary["boxy (single binary)"]
    Binary --> Serve["boxy serve<br/>─────────<br/>Daemon mode<br/>Pool reconciler<br/>REST + gRPC APIs"]
    Binary --> CLI["boxy &lt;command&gt;<br/>─────────<br/>CLI client<br/>Talks to daemon<br/>via REST"]
    Binary --> Agent["boxy agent<br/>─────────<br/>Remote agent<br/>Connects via gRPC<br/>Hosts local drivers"]
```

| Mode | Purpose | Transport |
|------|---------|-----------|
| `boxy serve` | Long-running daemon: reconciles pools, serves APIs | Listens on REST + gRPC |
| `boxy <cmd>` | CLI client: sandbox/pool management commands | Connects to daemon via REST |
| `boxy agent` | Remote agent on a different host | Dials server via gRPC/TLS |

---

## Domain Model

These are the core nouns in the system. All defined in `pkg/model/`.

```mermaid
erDiagram
    Pool ||--o{ Resource : "inventory contains"
    Sandbox ||--o{ Resource : "references (by ID)"
    Resource }o--|| ProviderRef : "belongs to"
    Pool ||--|| PoolPolicies : "has"
    Sandbox ||--|| SandboxPolicies : "has"
    PoolPolicies ||--|| PreheatPolicy : "has"
    PoolPolicies ||--|| RecyclePolicy : "has"

    Pool {
        PoolName name PK
        PoolPolicies policies
        ResourceCollection inventory
    }

    Resource {
        ResourceID id PK
        ResourceType type "vm | container | share | network | db"
        ResourceProfile profile
        ResourceState state "provisioning | ready | allocated | released | destroying | destroyed | error"
        ProviderRef provider
        map properties "opaque driver data"
        time created_at
        time updated_at
    }

    Sandbox {
        SandboxID id PK
        string name
        SandboxPolicies policies
        ResourceID[] resources
    }

    ProviderRef {
        string name
        string type
    }
```

### Resource Lifecycle

Resources flow through a one-way lifecycle. Once allocated to a sandbox, they
**never** return to a pool (see [ADR-0002](adr/0002-no-resource-recycling.md)).

```mermaid
stateDiagram-v2
    [*] --> provisioning : PolicyController triggers provisioning
    provisioning --> ready : Driver.Create succeeds
    provisioning --> error : Driver.Create fails

    ready --> allocated : Sandbox takes from pool
    allocated --> released : Sandbox destroyed
    released --> destroying : Cleanup begins
    destroying --> destroyed : Driver.Delete succeeds
    destroyed --> [*]

    error --> destroying : Retry/cleanup
```

---

## Package Architecture

Boxy enforces a strict **`internal/` vs `pkg/` boundary** — the Go compiler
prevents external consumers from importing `internal/` packages.

```mermaid
graph TB
    subgraph "cmd/ — Entry Points"
        boxy["cmd/boxy/main.go"]
        devfactory["cmd/devfactory/main.go"]
        schemagen["cmd/schema-gen/main.go"]
    end

    subgraph "internal/ — Private Business Logic"
        cli["cli/<br/>Cobra command wiring"]
        config["config/<br/>Config loading + pool/sandbox specs"]
        poolmgr["pool/<br/>Pool Manager + Provisioner"]
        sandboxmgr["sandbox/<br/>Sandbox Manager"]
    end

    subgraph "pkg/ — Public, Reusable Libraries"
        model["model/<br/>Core domain types"]
        storeif["store/<br/>Store interface + impls<br/>(memory, disk)"]
        providersdk["providersdk/<br/>Driver interface, Registry,<br/>Registration"]
        agentsdk["agentsdk/<br/>Agent interface + embedded impl"]
        policyctrl["policycontroller/<br/>Generic Observe→Decide→Act loop"]
        resourcepool["resourcepool/<br/>Generic homogeneous pool"]
    end

    subgraph "providersdk/providers/ — Drivers"
        docker["docker/<br/>Docker driver"]
        devfactorydrv["devfactory/<br/>DevFactory driver"]
    end

    boxy --> cli
    cli --> config
    cli --> poolmgr
    cli --> sandboxmgr
    cli --> storeif
    cli --> providersdk

    poolmgr --> model
    poolmgr --> storeif
    poolmgr --> policyctrl
    sandboxmgr --> model
    sandboxmgr --> storeif
    sandboxmgr --> resourcepool

    providersdk --> docker
    providersdk --> devfactorydrv

    agentsdk --> providersdk

    style model fill:#e1f5fe
    style policyctrl fill:#e8f5e9
    style providersdk fill:#fff3e0
    style agentsdk fill:#fce4ec
```

### Dependency Rule

> **`pkg/` packages have zero dependencies on `internal/`.**
>
> This is enforced by the Go compiler. `pkg/` packages are self-contained
> libraries that can be imported independently.

| Layer | Depends On | Purpose |
|-------|-----------|---------|
| `cmd/` | `internal/cli` | Binary entry points |
| `internal/cli` | `internal/*`, `pkg/*` | CLI wiring, server bootstrap |
| `internal/pool` | `pkg/model`, `pkg/store`, `pkg/policycontroller` | Pool reconciliation |
| `internal/sandbox` | `pkg/model`, `pkg/store`, `pkg/resourcepool` | Sandbox creation + allocation |
| `pkg/model` | stdlib only | Core domain types |
| `pkg/store` | `pkg/model` | Persistence interface |
| `pkg/policycontroller` | stdlib only | Generic control loop |
| `pkg/resourcepool` | stdlib only | Generic pool data structure |
| `pkg/providersdk` | stdlib only | Driver interface + registry |
| `pkg/agentsdk` | `pkg/providersdk` | Agent abstraction |

---

## Key Abstractions

### Policy Controller

The `policycontroller` package (`pkg/policycontroller/`) implements a generic
**Observe → Decide → Act** reconciliation loop using Go generics:

```mermaid
graph LR
    subgraph "Controller[T, P]"
        Observe["Observer[T]<br/>──────────<br/>Read current state"]
        Decide["Evaluator[T, P]<br/>──────────<br/>Compare to policy<br/>Produce Decision[P]"]
        Act["Actuator[P]<br/>──────────<br/>Execute the plan"]
    end

    Observe -->|"observed: T"| Decide
    Decide -->|"Decision{ShouldAct, Plan: P}"| Act

    Tick["Ticker / Reconcile()"] -.->|triggers| Observe
```

**Key properties:**
- **Generic over T (observed state) and P (plan)** — domain packages supply
  concrete types
- **Stateless and idempotent** — every tick re-derives what's needed from scratch
- Can run as a one-shot (`Reconcile()`) or in a loop (`Run(ctx, interval)`)
- Policy is not a separate input — it's embedded in the Evaluator implementation

The pool manager (`internal/pool/`) wires this up with concrete types:
- `T = observed{pool, now}` — the pool's inventory + current time
- `P = plan{pool, stale, toProvision, reason}` — what to destroy and provision

---

### Provider SDK & Driver Model

The `providersdk` package defines how Boxy talks to infrastructure:

```mermaid
graph TB
    subgraph "Provider SDK (pkg/providersdk/)"
        DriverIF["Driver interface<br/>──────────<br/>Create(ctx, cfg) → Resource<br/>Read(ctx, id) → Status<br/>Update(ctx, id, op) → Result<br/>Delete(ctx, id)"]

        Registry["Registry<br/>──────────<br/>Type → Registration mapping<br/>ConfigProto factory<br/>NewDriver factory"]

        Registration["Registration<br/>──────────<br/>Type: 'docker'<br/>ConfigProto: func() → DockerConfig{}<br/>NewDriver: func(cfg) → DockerDriver"]
    end

    subgraph "Concrete Drivers"
        DockerDriver["Docker Driver<br/>──────────<br/>Create → docker run<br/>Read → docker inspect<br/>Delete → docker rm"]
        DevFactory["DevFactory Driver<br/>──────────<br/>Profile-based provisioning<br/>Store management"]
    end

    Registry -->|"Get('docker')"| Registration
    Registration -->|"NewDriver(cfg)"| DockerDriver
    DriverIF -.->|implements| DockerDriver
    DriverIF -.->|implements| DevFactory
```

**Registration pattern:** Drivers self-register via a `Registration` struct
containing a `ConfigProto` factory (for YAML deserialization) and a `NewDriver`
factory. The `builtins` package calls `RegisterBuiltins(registry)` at startup
to wire all built-in drivers.

---

### Agent Layer

Agents are the **execution layer** — Boxy core never talks to drivers directly.

```mermaid
graph TB
    Server["Boxy Server<br/>(boxy serve)"]

    subgraph "Agent Interface (pkg/agentsdk/)"
        AgentIF["Agent interface<br/>──────────<br/>Info() → AgentInfo<br/>Create(provider, cfg)<br/>Read(provider, id)<br/>Update(provider, id, op)<br/>Delete(provider, id)"]
    end

    subgraph "Embedded Agent"
        EmbeddedAgent["EmbeddedAgent<br/>──────────<br/>In-process<br/>Direct driver calls"]
        LocalDrivers["Local Drivers<br/>(Docker, Hyper-V)"]
    end

    subgraph "Remote Agent (future)"
        RemoteAgent["RemoteAgent<br/>──────────<br/>gRPC client proxy<br/>Serializes ops"]
        RemoteHost["Remote Host<br/>gRPC → drivers"]
    end

    Server -->|"uses"| AgentIF
    AgentIF -.->|implements| EmbeddedAgent
    AgentIF -.->|implements| RemoteAgent
    EmbeddedAgent --> LocalDrivers
    RemoteAgent -->|"gRPC/TLS"| RemoteHost
```

**Transparency:** The server uses the `Agent` interface uniformly. Whether the
agent is embedded (in-process, direct function calls) or remote (gRPC proxy)
is transparent to the server code.

---

### Store Layer

```mermaid
graph LR
    subgraph "Store Interface (pkg/store/)"
        StoreIF["Store interface<br/>──────────<br/>GetPool / PutPool<br/>GetResource / PutResource<br/>GetSandbox / CreateSandbox / PutSandbox"]
    end

    MemStore["MemoryStore<br/>(in-memory maps)"]
    DiskStore["DiskStore<br/>(bbolt — planned)"]

    StoreIF -.->|implements| MemStore
    StoreIF -.->|implements| DiskStore
```

Currently ships with a `MemoryStore` for development. The planned production
store is **bbolt** (pure Go embedded key-value store). Config is NOT stored
in the database — it's read from `boxy.yaml` on startup.

---

## Reconciliation Flow

This is the core runtime loop. The PolicyController continuously compares
desired state (from pool policy) against actual state (from the store) and
takes corrective action.

```mermaid
sequenceDiagram
    participant Ticker as serveLoop ticker
    participant PC as PolicyController
    participant Observer as Observer
    participant Evaluator as Evaluator
    participant Actuator as Actuator
    participant Store as Store
    participant Agent as Agent/Provisioner

    Ticker->>PC: Reconcile()

    PC->>Observer: Observe(ctx)
    Observer->>Store: GetPool(name)
    Store-->>Observer: Pool{inventory, policies}
    Observer-->>PC: observed{pool, now}

    PC->>Evaluator: Evaluate(ctx, observed)
    Note over Evaluator: computeStale(): find resources past max_age<br/>computeToProvision(): gap = min_ready - ready_count
    Evaluator-->>PC: Decision{ShouldAct, Plan}

    alt ShouldAct == true
        PC->>Actuator: Act(ctx, plan)

        loop For each stale resource
            Actuator->>Agent: Destroy(pool, resource)
        end

        loop For each resource to provision
            Actuator->>Agent: Provision(pool)
            Agent-->>Actuator: new Resource
            Actuator->>Store: PutResource(resource)
        end

        Actuator->>Store: PutPool(updated pool)
    end
```

---

## Async Sandbox Fulfillment Flow

Sandbox creation is accepted immediately, then fulfilled asynchronously by the
daemon reconcile loop. Allocated resources are still **taken** from pools
(never returned).

```mermaid
sequenceDiagram
    participant User as User
    participant CLI as boxy CLI
    participant API as REST API
    participant Fulfiller as Sandbox Fulfiller
    participant SBM as Sandbox Manager
    participant Store as Store
    participant RP as ResourcePool

    User->>CLI: boxy sandbox create -f pentest-lab.sandbox.yaml
    CLI->>API: POST /api/v1/sandboxes {name, requests}
    API->>SBM: CreateRequested(name, policies, requests)
    SBM->>Store: CreateSandbox{id, status: pending, requests: [...]}
    API-->>CLI: 202 Accepted {id, status: pending}

    loop Reconcile tick
        Fulfiller->>Store: GetSandbox/ListPools
        Fulfiller->>Store: GetPool("kali")
        Store-->>Fulfiller: Pool{inventory: [r1,r2,r3,r4,r5]}

        Fulfiller->>SBM: AddFromPool("kali", 3)
        SBM->>RP: Take(3, filter=ready)
        RP-->>SBM: [r1, r2, r3]
        Note over RP: Pool inventory now: [r4, r5]

        SBM->>Store: PutPool(updated pool)

        loop For each selected resource
            SBM->>Store: PutResource(state=allocated)
        end

        SBM->>Store: PutSandbox{status: ready, resources:[r1,r2,r3]}
    end

    Note over SBM: Post-allocation hooks run here<br/>(set credentials, configure networking)

    CLI->>API: GET /api/v1/sandboxes/{id} until ready/failed
    API-->>CLI: Sandbox{id, status, resources}
    CLI-->>User: Connection info (SSH, RDP, etc.)

    Note over User: User connects directly with native client.<br/>Boxy is NOT a proxy.
```

---

## Data Flow

End-to-end data flow showing how configuration becomes running sandboxes:

```mermaid
graph TB
    subgraph "Configuration (static)"
        BoxyYaml["boxy.yaml<br/>──────────<br/>server settings<br/>pool definitions<br/>agent declarations"]
        SandboxYaml[".sandbox.yaml files<br/>──────────<br/>sandbox class definitions<br/>pool → count mappings"]
    end

    subgraph "Startup"
        ConfigLoad["Config Loader<br/>──────────<br/>Parse YAML<br/>Validate pool specs"]
        DriverReg["Driver Registry<br/>──────────<br/>Register builtins<br/>Validate provider instances"]
    end

    subgraph "Runtime (boxy serve)"
        RecLoop["Reconcile Loop (10s tick)<br/>──────────<br/>For each pool:<br/>Observe → Decide → Act"]
        PoolMgr["Pool Manager"]
        SandboxMgr["Sandbox Manager"]
        StoreRT["Store (bbolt / memory)"]
    end

    subgraph "Execution"
        EmbAgent["Embedded Agent"]
        RemAgent["Remote Agents"]
        Drivers["Provider Drivers<br/>(Docker, Hyper-V, ...)"]
    end

    subgraph "External Infrastructure"
        Docker["Docker Engine"]
        HyperV["Hyper-V Host"]
        Other["Other Providers..."]
    end

    BoxyYaml --> ConfigLoad
    ConfigLoad --> DriverReg
    DriverReg --> RecLoop

    RecLoop --> PoolMgr
    PoolMgr --> StoreRT
    PoolMgr -->|"Provision/Destroy"| EmbAgent
    PoolMgr -->|"Provision/Destroy"| RemAgent

    SandboxYaml -->|"boxy sandbox create -f"| SandboxMgr
    SandboxMgr --> StoreRT

    EmbAgent --> Drivers
    RemAgent -->|"gRPC/TLS"| Drivers

    Drivers --> Docker
    Drivers --> HyperV
    Drivers --> Other
```

---

## Configuration Model

Configuration is **declarative and stateless**. Runtime state lives in the store.

```mermaid
graph LR
    subgraph "boxy.yaml"
        Server["server:<br/>  listen: ':9090'<br/>  providers: [docker, hyperv]"]
        Agents["agents:<br/>  - name: build-host<br/>    providers: [docker]"]
        Pools["pools:<br/>  - name: win2022-base<br/>    type: hyperv<br/>    config: {template, cpu, ...}<br/>    policy: {preheat, recycle}"]
    end

    subgraph "*.sandbox.yaml"
        SBDef["name: pentest-lab<br/>resources:<br/>  - pool: kali, count: 3<br/>  - pool: ubuntu, count: 1"]
    end

    Server -->|"embedded agent providers"| Runtime["Runtime"]
    Agents -->|"expected remote agents"| Runtime
    Pools -->|"pool definitions + policies"| Runtime
    SBDef -->|"sandbox creation (CLI)"| Runtime
```

**Key design decisions:**
- Pool `config:` is an **opaque blob** — interpreted only by the driver, not by Boxy core
- Pool `type:` maps directly to a driver (e.g., `type: docker` → Docker driver)
- No separate "blueprint" or "template" entity — the pool IS the spec
- Sandbox definitions are separate files, version-controllable

---

## Technology Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| Language | **Go 1.22** | Core implementation |
| CLI Framework | **Cobra** (`spf13/cobra`) | Command-line interface |
| Config Format | **YAML** (`gopkg.in/yaml.v3`) | Configuration files |
| State Store | **bbolt** (planned) / Memory (current) | Runtime state persistence |
| Server-Agent RPC | **gRPC over TLS** (planned) | Agent-server communication |
| Client-Server API | **REST/HTTP** (planned) | CLI-server communication |
| Auth | **JWT** (planned) | Agent registration + auth |
| Task Runner | **Taskfile** (`Taskfile.yml`) | Build and development tasks |

### Project Status

Boxy is in **early development**. The domain model, policy controller, provider
SDK, and pool/sandbox managers are implemented. The REST API, gRPC transport,
and remote agent support are on the roadmap.

---

## Architectural Decision Records

- [ADR-0001: Resource Identity and Provider Handle](adr/0001-resource-identity-and-provider-handle.md) — (Deprecated) Early discussion on resource identity
- [ADR-0002: Resources Never Return to a Pool](adr/0002-no-resource-recycling.md) — Resources are single-use; pools only hold unused inventory
