# Boxy v1 Complete Architecture Map

**Version:** v1.0
**Date:** 2024-11-22
**Purpose:** Complete system architecture for review and planning

For the overarching project vision and other phases, please refer to the [Boxy Development Roadmap](../ROADMAP.md).

---

## Full System Architecture

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                           USER INTERFACES                                    │
│                                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │     CLI      │  │   Web UI     │  │  REST API    │  │   SDK/LIB    │   │
│  │              │  │              │  │              │  │              │   │
│  │ boxy pool    │  │ Dashboard    │  │ HTTP/JSON    │  │ Go Client    │   │
│  │ boxy sandbox │  │ Pool Mgmt    │  │ Auth: Token  │  │ Programmatic │   │
│  │ boxy admin   │  │ (v2)         │  │              │  │ (v2)         │   │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘   │
│         │                 │                 │                 │            │
│         └─────────────────┴─────────────────┴─────────────────┘            │
└─────────────────────────────────────┬───────────────────────────────────────┘
                                      │
                                      ↓
┌─────────────────────────────────────────────────────────────────────────────┐
│                        AUTHENTICATION & AUTHORIZATION                        │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │  Auth Middleware: API Token Validation                               │  │
│  │  - Validates token from header/config                                │  │
│  │  - Extracts user identity                                            │  │
│  │  - Enforces quotas                                                   │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────┬───────────────────────────────────────┘
                                      │
                                      ↓
┌─────────────────────────────────────────────────────────────────────────────┐
│                          BOXY SERVICE CORE                                   │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                      REQUEST HANDLER                                   │ │
│  │  - Routes commands to appropriate managers                            │ │
│  │  - Validates input                                                    │ │
│  │  - Handles errors                                                     │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  ┌─────────────────────┐        ┌─────────────────────┐                    │
│  │   USER MANAGER      │        │   TEAM MANAGER      │                    │
│  │   (v1)              │        │   (v1 optional)     │                    │
│  │                     │        │                     │                    │
│  │ • Create users      │        │ • Create teams      │                    │
│  │ • Generate tokens   │        │ • Manage members    │                    │
│  │ • Manage quotas     │        │ • Enforce quotas    │                    │
│  └─────────────────────┘        └─────────────────────┘                    │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                    FIRST-CLASS COMPONENTS                              │ │
│  │                    (User-Facing, Equal Peers)                          │ │
│  │                                                                        │ │
│  │  ┌──────────────────────────┐      ┌──────────────────────────┐      │ │
│  │  │     POOL MANAGER         │      │   SANDBOX MANAGER        │      │ │
│  │  │                          │      │                          │      │ │
│  │  │ Manages UNALLOCATED      │      │ Manages ALLOCATED        │      │ │
│  │  │ resources                │      │ resources                │      │ │
│  │  │                          │      │                          │      │ │
│  │  │ • Provision resources    │      │ • Create sandboxes       │      │ │
│  │  │ • Run on_provision hooks │      │ • Track expiration       │      │ │
│  │  │ • Maintain min_ready     │      │ • Multi-resource coord   │      │ │
│  │  │ • Health checking        │      │ • Auto-cleanup           │      │ │
│  │  │ • Preheating (warm)      │      │ • Extend duration        │      │ │
│  │  │ • Recycling (refresh)    │      │                          │      │ │
│  │  │                          │      │                          │      │ │
│  │  │ Workers:                 │      │ Workers:                 │      │ │
│  │  │ - Replenishment          │      │ - Cleanup (expired)      │      │ │
│  │  │ - Health check           │      │                          │      │ │
│  │  │ - Preheating             │      │                          │      │ │
│  │  │ - Recycling              │      │                          │      │ │
│  │  └────────┬─────────────────┘      └────────┬─────────────────┘      │ │
│  │           │                                  │                        │ │
│  │           └──────────────┬───────────────────┘                        │ │
│  │                          ↓                                            │ │
│  │           ┌──────────────────────────────────────┐                    │ │
│  │           │         ALLOCATOR                    │                    │ │
│  │           │         (Internal Orchestrator)      │                    │ │
│  │           │                                      │                    │ │
│  │           │ NOT user-facing - coordinates:      │                    │ │
│  │           │ • Resource ownership tracking        │                    │ │
│  │           │ • Pool → Sandbox allocation          │                    │ │
│  │           │ • Runs on_allocate hooks             │                    │ │
│  │           │ • Resource release (destroy)         │                    │ │
│  │           │ • Query interface for both           │                    │ │
│  │           │                                      │                    │ │
│  │           │ Single source of truth for:          │                    │ │
│  │           │ - Which pool owns which resources    │                    │ │
│  │           │ - Which sandbox owns which resources │                    │ │
│  │           └──────────────┬───────────────────────┘                    │ │
│  │                          ↓                                            │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                    HOOK EXECUTOR                                       │ │
│  │                                                                        │ │
│  │  Executes lifecycle hooks:                                            │ │
│  │  • on_provision: Heavy setup (slow, pool warming)                     │ │
│  │  • on_allocate: User personalization (fast, user waiting)             │ │
│  │                                                                        │ │
│  │  Via Provider.Exec():                                                 │ │
│  │  - PowerShell scripts (Windows)                                       │ │
│  │  - Bash scripts (Linux containers/VMs)                                │ │
│  │  - Python scripts                                                     │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────┬───────────────────────────────────────┘
                                      │
                                      ↓
┌─────────────────────────────────────────────────────────────────────────────┐
│                        STORAGE & PERSISTENCE                                 │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │  RESOURCE REPOSITORY (SQLite / PostgreSQL)                             │ │
│  │                                                                        │ │
│  │  Tables:                                                               │ │
│  │  • resources (id, pool_id, sandbox_id, state, provider_id, metadata)  │ │
│  │  • sandboxes (id, name, user_id, team_id, state, expires_at)          │ │
│  │  • users (id, username, api_token, role)                              │ │
│  │  • teams (id, name, members, quotas) [optional v1]                    │ │
│  │                                                                        │ │
│  │  Queries:                                                              │ │
│  │  - Get available resources for pool                                   │ │
│  │  - Get allocated resources for sandbox                                │ │
│  │  - Count resources by state                                           │ │
│  │  - Get expired sandboxes                                              │ │
│  │  - User authentication by token                                       │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │  CONFIGURATION STORE                                                   │ │
│  │                                                                        │ │
│  │  ~/.config/boxy/boxy.yaml                                              │ │
│  │  - Pool definitions                                                    │ │
│  │  - Provider settings                                                   │ │
│  │  - Preheating config                                                   │ │
│  │  - Hook definitions                                                    │ │
│  │  - Server settings                                                     │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────┬───────────────────────────────────────┘
                                      │
                                      ↓
┌─────────────────────────────────────────────────────────────────────────────┐
│                          PROVIDER LAYER                                      │
│                          (Stateless, Dumb CRUD)                              │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                      PROVIDER REGISTRY                                 │ │
│  │  Thread-safe map of available providers                               │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │   DOCKER     │  │   HYPER-V    │  │     KVM      │  │   VMWARE     │   │
│  │   PROVIDER   │  │   PROVIDER   │  │   PROVIDER   │  │   PROVIDER   │   │
│  │              │  │              │  │              │  │              │   │
│  │ Local:       │  │ Local/Remote:│  │ Local:       │  │ Remote:      │   │
│  │ - Embedded   │  │ - Embedded   │  │ - Embedded   │  │ - Via agent  │   │
│  │ - Docker API │  │ - PowerShell │  │ - libvirt    │  │ - gRPC       │   │
│  │              │  │ - COM/WMI    │  │              │  │              │   │
│  │              │  │ Remote:      │  │              │  │              │   │
│  │              │  │ - Via agent  │  │              │  │              │   │
│  │              │  │ - gRPC (v2)  │  │              │  │              │   │
│  │              │  │              │  │              │  │              │   │
│  │ Methods:     │  │ Methods:     │  │ Methods:     │  │ Methods:     │   │
│  │ • Provision  │  │ • Provision  │  │ • Provision  │  │ • Provision  │   │
│  │ • Destroy    │  │ • Destroy    │  │ • Destroy    │  │ • Destroy    │   │
│  │ • GetStatus  │  │ • GetStatus  │  │ • GetStatus  │  │ • GetStatus  │   │
│  │ • GetConnInfo│  │ • GetConnInfo│  │ • GetConnInfo│  │ • GetConnInfo│   │
│  │ • Update     │  │ • Update     │  │ • Update     │  │ • Update     │   │
│  │ • Exec       │  │ • Exec       │  │ • Exec       │  │ • Exec       │   │
│  │ • HealthCheck│  │ • HealthCheck│  │ • HealthCheck│  │ • HealthCheck│   │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘   │
│         │                 │                 │                 │            │
│         └─────────────────┴─────────────────┴─────────────────┘            │
└─────────────────────────────────────┬───────────────────────────────────────┘
                                      │
                                      ↓
┌─────────────────────────────────────────────────────────────────────────────┐
│                    BACKEND INFRASTRUCTURE                                    │
│                                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │   Docker     │  │   Hyper-V    │  │     KVM      │  │   VMware     │   │
│  │   Daemon     │  │   Host       │  │   Host       │  │   vCenter    │   │
│  │              │  │              │  │              │  │              │   │
│  │ Containers   │  │ VMs          │  │ VMs          │  │ VMs          │   │
│  │ Images       │  │ VHDXs        │  │ QCOWs        │  │ VMDKs        │   │
│  │ Networks     │  │ Switches     │  │ Bridges      │  │ Port Groups  │   │
│  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Resource State Flow

```text
┌─────────────────────────────────────────────────────────────────────┐
│                        RESOURCE LIFECYCLE                            │
└─────────────────────────────────────────────────────────────────────┘

Provider.Provision()
       ↓
┌──────────────┐
│ Provisioned  │ ← Resource created but COLD (stopped/not running)
│ (Cold)       │   Owner: Pool
└──────┬───────┘   Location: Pool inventory
       │
       │ on_provision hooks run (validation, snapshots)
       │
       ├─────→ If preheating disabled: stays here
       │       Allocated when requested (slow path: 30-60s)
       │
       │ If preheating enabled:
       ↓
┌──────────────┐
│   Warming    │ ← Provider.Update(PowerState: Running)
│              │   Starting up...
└──────┬───────┘
       │
       ↓
┌──────────────┐
│    Ready     │ ← Resource running and WARM (preheated)
│   (Warm)     │   Owner: Pool
└──────┬───────┘   Location: Pool inventory
       │           Available for instant allocation (< 5s)
       │
       │ User requests allocation
       │ Allocator.AllocateFromPool()
       ↓
┌──────────────┐
│ Allocating   │ ← on_allocate hooks running
│              │   (create user, grant access)
└──────┬───────┘
       │
       ↓
┌──────────────┐
│  Allocated   │ ← Resource in use
│              │   Owner: Sandbox
└──────┬───────┘   Location: Sandbox
       │           User has access
       │
       │ Sandbox expires or user destroys
       │ Allocator.ReleaseResources()
       ↓
┌──────────────┐
│  Destroyed   │ ← Provider.Destroy()
│              │   Resource deleted
└──────────────┘   Owner: None
                   Never reused - always clean!

Recycling (parallel flow):
┌──────────────┐
│ Ready/       │ ← After recycle_interval
│ Provisioned  │   Pool.RecycleResources()
└──────┬───────┘
       │
       ↓
┌──────────────┐
│  Recycling   │ ← Destroy old, provision new
│              │   Keeps resources fresh
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ Provisioned  │ ← Back to pool inventory
│ (Fresh)      │   (cold or warm based on config)
└──────────────┘
```

---

## Data Flow: Sandbox Creation

```text
┌─────────────────────────────────────────────────────────────────────┐
│            USER CREATES SANDBOX (Full Flow)                          │
└─────────────────────────────────────────────────────────────────────┘

User: boxy sandbox create -p win11-test:1 -d 1h
       ↓
┌──────────────────┐
│   CLI Handler    │ ← Parses command
└────────┬─────────┘
         ↓
┌──────────────────┐
│ Auth Middleware  │ ← Validates API token
│                  │   Extracts user identity
│                  │   Checks quota
└────────┬─────────┘
         ↓
┌──────────────────┐
│ Sandbox Manager  │ ← Create(req, userID)
│                  │   Creates sandbox record
└────────┬─────────┘   State: Creating
         │
         │ Spawns async worker
         ↓
┌──────────────────┐
│ Allocator        │ ← AllocateFromPool(pool, sandboxID)
│                  │
│ 1. Query pool    │ ← Pool.GetAvailableResources()
│    for resources │   Returns: Ready resources (warm)
│                  │
│ 2. Select        │ ← Pick first available
│    resource      │
│                  │
│ 3. Run hooks     │ ← HookExecutor.ExecuteHooks(on_allocate)
│    (if any)      │   - Create user account
│                  │   - Grant RDP/SSH access
│                  │   - Set hostname
│                  │
│ 4. Update        │ ← res.State = Allocated
│    resource      │   res.SandboxID = sandboxID
│                  │   Repository.Update(res)
│                  │
│ 5. Return to     │ ← Return resource to caller
│    sandbox mgr   │
└────────┬─────────┘
         │
         ↓
┌──────────────────┐
│ Sandbox Manager  │ ← Updates sandbox record
│                  │   sb.ResourceIDs = [res.ID]
└────────┬─────────┘   sb.State = Ready
         │
         ↓
┌──────────────────┐
│ Pool Manager     │ ← Triggered by resource removal
│ (background)     │   ensureMinReady()
└────────┬─────────┘   Provisions replacement
         │
         ↓
┌──────────────────┐
│ Provider         │ ← Provision new resource
│                  │   State: Provisioned (cold)
└────────┬─────────┘
         │
         ↓
┌──────────────────┐
│ Preheating       │ ← If enabled, warm it up
│ Worker           │   Provider.Update(PowerState: Running)
└────────┬─────────┘   State: Ready (warm)
         │
         ↓
Pool replenished! ✅

Meanwhile...

User: boxy sandbox get <sandbox-id>
       ↓
┌──────────────────┐
│ Sandbox Manager  │ ← GetResourcesForSandbox(sandboxID)
│                  │
└────────┬─────────┘
         ↓
┌──────────────────┐
│ Allocator        │ ← Query allocated resources
│                  │   Join with Provider.GetConnectionInfo()
└────────┬─────────┘
         │
         ↓
User receives:
{
  "sandbox_id": "sb-123",
  "state": "ready",
  "resources": [
    {
      "id": "res-abc",
      "type": "vm",
      "connection": {
        "host": "192.168.1.100",
        "port": 3389,
        "protocol": "rdp",
        "username": "boxy-user",
        "password": "XXXXXX"  // Auto-generated, secure
      }
    }
  ],
  "expires_at": "2025-11-22T14:00:00Z"
}

User connects via RDP → Uses clean VM → Done ✅

After 1 hour (auto-expiration):
       ↓
┌──────────────────┐
│ Cleanup Worker   │ ← GetExpiredSandboxes()
│ (30s interval)   │   Finds sb-123
└────────┬─────────┘
         ↓
┌──────────────────┐
│ Sandbox Manager  │ ← Destroy(sb-123)
│                  │
└────────┬─────────┘
         ↓
┌──────────────────┐
│ Allocator        │ ← ReleaseResources(sb-123)
│                  │
└────────┬─────────┘
         ↓
┌──────────────────┐
│ Provider         │ ← Destroy(res-abc)
│                  │   VM deleted
└────────┬─────────┘
         │
         ↓
Resource destroyed ✅
Sandbox marked destroyed ✅
Pool replenishes automatically ✅
```

---

## Cold vs Warm Allocation

```text
┌─────────────────────────────────────────────────────────────────────┐
│               PREHEATING: COLD VS WARM PATHS                         │
└─────────────────────────────────────────────────────────────────────┘

Pool Configuration:
  min_ready: 10          # Total resources
  max_total: 20
  preheating:
    enabled: true
    count: 3             # Keep 3 warm

Pool State:
  ├─ Cold (Provisioned): 7 resources (stopped)
  └─ Warm (Ready):       3 resources (running)

User Request: boxy sandbox create -p pool:1

┌─────────────────────┐
│   WARM PATH         │ ← If warm resource available
│   (Fast: < 5s)      │
└─────────────────────┘
         │
         ├→ Pick warm resource (already running)
         ├→ Run on_allocate hooks (user account, etc)
         ├→ Return to user immediately ✅
         └→ Background: Provision replacement (cold)
                        Preheat one more (cold → warm)

┌─────────────────────┐
│   COLD PATH         │ ← If no warm resources
│   (Slower: 30-60s)  │
└─────────────────────┘
         │
         ├→ Pick cold resource (stopped)
         ├→ Provider.Update(PowerState: Running)  [30-60s]
         ├→ Wait for startup
         ├→ Run on_allocate hooks
         ├→ Return to user ✅
         └→ Background: Provision replacement

Cost/Speed Tradeoff:
┌────────────┬───────────┬──────────┬─────────────┐
│ Config     │ Cost      │ Speed    │ Best For    │
├────────────┼───────────┼──────────┼─────────────┤
│ All Cold   │ Lowest    │ Slow     │ Dev/Test    │
│ (count: 0) │           │ 30-60s   │ Low demand  │
├────────────┼───────────┼──────────┼─────────────┤
│ Some Warm  │ Medium    │ Fast*    │ Production  │
│ (count: 3) │           │ <5s      │ Balanced    │
├────────────┼───────────┼──────────┼─────────────┤
│ All Warm   │ Highest   │ Fastest  │ High demand │
│ (count: 10)│           │ <5s      │ Peak hours  │
└────────────┴───────────┴──────────┴─────────────┘

* Fast for first 3 requests, then slower until replenished
```

---

## Distributed Architecture (v2 - Future)

```text
┌─────────────────────────────────────────────────────────────────────┐
│          DISTRIBUTED DEPLOYMENT (v2)                                 │
│          Boxy Server on Linux, Hyper-V Agent on Windows              │
└─────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────┐
│   Boxy Server (Linux Host)           │
│                                      │
│   ┌──────────────────────────────┐  │
│   │  Pool Manager                │  │
│   │  - Manages win-test-vms pool │  │
│   │  - backend: hyperv           │  │
│   │  - backend_agent: win-host-01│  │
│   └──────────┬───────────────────┘  │
│              │                      │
│   ┌──────────▼───────────────────┐  │
│   │  RemoteProvider (gRPC client)│  │
│   │  - Connects to agent         │  │
│   │  - Transparent interface     │  │
│   └──────────┬───────────────────┘  │
└──────────────┼──────────────────────┘
               │
               │ gRPC + mTLS
               │ (TLS 1.3, Client Certs)
               ↓
┌──────────────────────────────────────┐
│   Boxy Agent (Windows Host)          │
│                                      │
│   ┌──────────────────────────────┐  │
│   │  Agent Server (gRPC)         │  │
│   │  - Listens on port 8444      │  │
│   │  - mTLS authentication       │  │
│   └──────────┬───────────────────┘  │
│              │                      │
│   ┌──────────▼───────────────────┐  │
│   │  Hyper-V Provider (embedded) │  │
│   │  - PowerShell/WMI            │  │
│   │  - Local execution           │  │
│   └──────────┬───────────────────┘  │
│              │                      │
│   ┌──────────▼───────────────────┐  │
│   │  Hyper-V Host                │  │
│   │  - VMs, VHDXs, vSwitches     │  │
│   └──────────────────────────────┘  │
└──────────────────────────────────────┘

Flow:
1. User: boxy sandbox create -p win-test-vms:1
2. Server Pool Manager → RemoteProvider
3. RemoteProvider → gRPC call → Agent Server
4. Agent Server → Local Hyper-V Provider
5. Hyper-V Provider → Create VM locally
6. Response flows back to user

Benefits:
- Server runs on Linux (cheaper, easier ops)
- Agent runs on Windows (where Hyper-V requires)
- Secure communication (mTLS)
- Scalable (multiple agents)
```

---

## Key Principles Illustrated

1. **Separation of Concerns**
   - Pool manages unallocated
   - Sandbox manages allocated
   - Allocator orchestrates between them
       - See also: [architecture/MVP_DESIGN.md](architecture/MVP_DESIGN.md) for v1-prerelease design principles.

2. **Single Source of Truth**
   - Allocator owns resource ownership tracking
   - Repository stores persistent state
   - No dual ownership

3. **User-Facing vs Internal**
   - Pool, Sandbox: User can interact via CLI/API
   - Allocator: Internal only, transparent to users

4. **Provider Abstraction**
   - Providers are dumb CRUD interfaces
   - No logic, no state
   - Easily swappable
   - See also: [decisions/adr-002-provider-architecture.md](decisions/adr-002-provider-architecture.md) for provider architecture decisions.

5. **Hook System**
   - on_provision: Heavy setup (pool warming)
   - on_allocate: User personalization (fast)
   - See also: [architecture/HOOKS.md](architecture/HOOKS.md) for detailed hook design.

6. **Resource Lifecycle**
   - Cold → Warm → Allocated → Destroyed
   - Never reused - always clean!
   - Recycling prevents drift

7. **Preheating**
   - Cost/speed tradeoff made explicit
   - Configurable per pool
   - Warm resources = instant allocation

---

**Document Purpose:**

This document provides a complete architectural map for planning, design, and review. Use this to understand the entire system at a glance and identify how components interact. For more detail on specific areas, refer to the documents in the `architecture/` and `decisions/` directories.
