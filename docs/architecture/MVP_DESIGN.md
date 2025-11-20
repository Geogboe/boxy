# MVP Design Document

This document captures ALL architectural decisions made for Boxy MVP.

## Core Paradigm

**Boxy is "serverless compute" for ephemeral sandbox environments across heterogeneous backends.**

**Key innovations**:
- **Warm pools**: Resources always ready (no wait time)
- **Auto-cleanup**: Time-bound resources, no orphans
- **Heterogeneous**: Docker + Hyper-V unified
- **Distributed**: Server orchestrates, agents provide remote capabilities

**Mental model**: "Vending machine" - request resource, get it instantly, use it, auto-expires.

## Component Roles

### Provider
**Dumb, stateless CRUD interface**:
- No decisions, no state management
- Just translates Boxy commands to backend APIs
- Methods: Provision, Destroy, GetStatus, GetConnectionInfo, Execute, Update, HealthCheck

**Execute() purpose**: Used by hooks to run setup scripts inside resources

**Update() purpose**: Generic modifications (power state, snapshot, resource limits)

###Agent
**Pure proxy, no autonomy**:
- No decision making, no state
- Polls server or responds to RPC
- Just remote execution of provider methods
- Uses mTLS with client certificates

### Server
**Orchestrator (the brain)**:
- Manages pools and sandboxes
- Tracks state and expiration
- Routes requests to providers (local or remote)
- Runs lifecycle hooks

### Hooks
**Lifecycle scripts at specific points**:
- **after_provision** (finalization): Slow setup during pool warming
- **before_allocate** (personalization): Fast user-specific setup
- Shell scripts: bash, powershell, python

## Two-Phase Provisioning

### Phase 1: Finalization (Pool Warming)
```
Base Image → Provider.Provision() → Hooks (after_provision) → Ready → Pool
```

**Purpose**: Prepare base image for pool
**Timing**: Background, can be slow (minutes)
**User involvement**: None (happens automatically)

**Common tasks**:
- Network validation
- Optional software installation (user scripts)
- Health checks

**Base image expectations**:
- Should be mostly ready
- Hooks for optional finalization only
- Heavy setup should be in base image

### Phase 2: Personalization (Allocation)
```
Pool Resource → Hooks (before_allocate) → User
```

**Purpose**: Make resource unique for user
**Timing**: User waiting, must be fast (seconds)
**User involvement**: User requested resource

**Common tasks**:
- Create user account with random password
- Set unique hostname
- Quick customization

**NOT for heavy operations** - if it's slow, put it in base image or finalization.

## Resource Lifecycle States

```
StateProvisioning → StateReady → StateAllocated → StateDestroyed
                        ↑             ↓
                     (in pool)   (user has it)
```

**State transitions**:
1. `Provisioning`: Provider creating + finalization hooks running
2. `Ready`: In pool, available for allocation
3. `Allocated`: User has it, expires after duration
4. `Destroyed`: Cleaned up, gone forever

**No reuse**: Resources never return to pool (vending machine model)

## Pools vs Sandboxes

### Pool
**Inventory of pre-provisioned resources of same type**:
```yaml
Pool "win-server-vms":
  Config: min_ready=3, max_total=10
  Resources: [vm1-ready, vm2-ready, vm3-ready, vm4-provisioning...]
```

**Responsibilities**:
- Maintain min_ready count
- Replenish when resources allocated
- Run finalization hooks on new resources

### Sandbox
**Logical wrapper around allocated resource(s)**:
```yaml
Sandbox "sb-123":
  Resources: [res-abc from pool "win-server-vms"]
  Allocated: 2025-11-20 10:00
  Expires: 2025-11-20 18:00
  Status: ready
```

**Responsibilities**:
- Track which resource(s) belong together
- Handle expiration
- Provide connection info to user

**MVP scope**: One resource per sandbox
**MVP2 scope**: Multiple resources per sandbox

## Async Allocation Model

Allocation may take time (hooks running), so it's async:

**CLI behavior** (auto-waits):
```bash
$ boxy sandbox create -p win-server-vms:1 -d 8h

Provisioning... ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 30s
Finalizing... ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 45s

✓ Ready! sb-xyz789
```

**API behavior** (immediate response):
```
POST /api/v1/sandboxes → 202 Accepted (status: provisioning)
GET /api/v1/sandboxes/:id → 200 OK (status: ready)
```

**CLI flag**: `--no-wait` to skip auto-waiting

## Credentials & Security

### Credential Modes

```yaml
pools:
  - name: my-pool

    credentials:
      # Auto-generate (default)
      mode: auto
      username: Administrator  # Fixed username, password auto-generated

      # Fixed credentials
      mode: fixed
      username: admin
      password: ${env:POOL_PASSWORD}

      # SSH keys
      mode: ssh_key
      username: ubuntu
      ssh_key: ${file:~/.ssh/id_rsa.pub}
```

### Token-Based Agent Registration

**Not manual certificate distribution!**

1. Admin generates token: `boxy admin generate-agent --providers hyperv`
2. Admin distributes CA cert + token to agent host
3. Agent installs: `boxy agent install --server https://... --ca ca.pem --token <token>`
4. Agent exchanges token for certificate (server generates)
5. All future communication uses mTLS with that certificate

**Benefits**: Simple, secure, auditable

## Image Configuration

### Base Images

```yaml
pools:
  - name: win-server-vms

    image:
      source: C:\Images\win-server-2022-base.vhdx

      # Optional: Start from snapshot
      snapshot: "Clean-State"

      # Optional: Use differencing disks (Hyper-V)
      differencing_disk: true  # Instant pool replenishment
```

**Differencing disks** (Hyper-V specific):
- Base VHDX is read-only, shared
- Each VM gets differencing disk (changes only)
- Pool replenishment is instant (just create diff disk)
- Massive performance improvement

**Docker**: Layers work like differencing disks automatically

## Configuration File Structure

### Pool Configuration

```yaml
storage:
  type: sqlite
  path: ~/.config/boxy/boxy.db

pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    # backend_agent: (not specified = local)
    image:
      source: ubuntu:22.04

    min_ready: 5
    max_total: 20
    cpus: 2
    memory_mb: 1024

    credentials:
      mode: auto
      username: ubuntu

    timeouts:
      provision: 300s
      finalization: 600s
      personalization: 30s
      destroy: 60s

    hooks:
      after_provision:
        - type: script
          shell: bash
          inline: |
            apt-get update
            apt-get install -y curl
          timeout: 120s

      before_allocate:
        - type: script
          shell: bash
          inline: |
            useradd -m ${username}
            echo "${username}:${password}" | chpasswd
          timeout: 10s
```

### Server Configuration

```yaml
server:
  listen_address: 0.0.0.0:8443

  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca-cert.pem
    client_auth: require

# Remote agents
agents:
  - id: windows-host-01
    address: windows-host-01.internal:8444
    providers:
      - hyperv
    max_resources: 50
```

### Client Configuration

```yaml
# ~/.config/boxy/config.yaml
server: https://boxy-server.internal:8443
token: bxy_abc123xyz789  # API token for auth

# Or environment variables
# BOXY_SERVER=https://...
# BOXY_TOKEN=bxy_...

# Or flags
# boxy --server https://... --token bxy_...
```

**Precedence**: flag > env > config file

## Authentication

### Local (Same Host)

**Linux/Mac**: Unix socket `/var/run/boxy.sock` (no auth needed)
**Windows**: Named pipe `\\.\pipe\boxy` (no auth needed)

**Go stdlib supports both** via `net.Listen("unix", ...)` and `net.Listen("np", ...)`

### Remote

**TCP with TLS + API tokens**:
```
Client → Server: HTTPS with API token in header
Server validates token
```

**Token generation**:
```bash
boxy admin create-token --name john-cli --expires 90d
→ bxy_abc123xyz789
```

## CLI Commands

### Core Commands

```bash
# Server
boxy serve                          # Start server
boxy admin init-ca                  # Initialize CA
boxy admin generate-agent           # Generate agent token
boxy admin create-token             # Create API token

# Agent
boxy agent install --server ... --ca ... --token ...
boxy agent start
boxy agent stop
boxy agent status

# Pools
boxy pool ls
boxy pool stats <name>

# Sandboxes
boxy sandbox create -p <pool>:<count> -d <duration>
boxy sandbox ls
boxy sandbox get <id>
boxy sandbox extend <id> -d <duration>
boxy sandbox destroy <id>

# Debugging
boxy admin pool test-finalization <pool> --debug
boxy admin resource test-personalization <res> --debug
```

### CLI/API Parity

**Every CLI command maps to API endpoint**:
```
boxy sandbox create → POST /api/v1/sandboxes
boxy sandbox ls → GET /api/v1/sandboxes
boxy sandbox get <id> → GET /api/v1/sandboxes/:id
boxy sandbox destroy <id> → DELETE /api/v1/sandboxes/:id
```

## Provider Interface

```go
type Provider interface {
    // Lifecycle
    Provision(ctx context.Context, spec ResourceSpec) (*Resource, error)
    Destroy(ctx context.Context, res *Resource) error

    // Status & Info
    GetStatus(ctx context.Context, res *Resource) (*ResourceStatus, error)
    GetConnectionInfo(ctx context.Context, res *Resource) (*ConnectionInfo, error)

    // Management
    Update(ctx context.Context, res *Resource, updates ResourceUpdate) error
    Execute(ctx context.Context, res *Resource, cmd []string) (*ExecuteResult, error)

    // Health
    HealthCheck(ctx context.Context) error

    // Metadata
    Name() string
    Type() ResourceType
}

type ResourceUpdate struct {
    PowerState  *PowerState       // VMs: Running, Stopped, Paused
    Snapshot    *SnapshotOp       // VMs: Create/restore snapshot
    Resources   *ResourceLimits   // Containers: CPU/memory
    // Provider interprets what it supports
}
```

## MVP Scope

### In Scope

**Core Functionality**:
- ✅ Pool management with warm pools
- ✅ Hook-based provisioning (after_provision, before_allocate)
- ✅ Async allocation with auto-wait CLI
- ✅ Docker provider (real implementation)
- ✅ Hyper-V provider (stub for testing)
- ✅ Token-based agent registration
- ✅ Auto-generated credentials
- ✅ Time-bound sandboxes with extension
- ✅ Manual cleanup (no auto-cleanup of unhealthy)

**Testing**:
- ✅ Unit tests for all components
- ✅ Integration tests with Docker
- ✅ E2E tests with Docker
- ✅ Stub Hyper-V for cross-platform testing

**Commands**:
- ✅ `boxy serve`, `boxy agent`, `boxy admin`
- ✅ `boxy pool ls/stats`
- ✅ `boxy sandbox create/ls/get/extend/destroy`
- ✅ Debug commands for testing hooks

### Explicitly Out of Scope (Later)

**MVP2**:
- Multi-resource sandboxes (Docker + Hyper-V in one sandbox)
- Overlay networking (WireGuard/Headscale)
- Sandbox-as-code (declarative YAML)
- Domain join hooks
- Complex provisioners (Ansible, DSC)

**Future**:
- Auto-cleanup of unhealthy resources
- Tag-based provider selection
- Capacity-aware scheduling
- Multi-agent load balancing
- Web UI
- Multi-tenancy

## Testing Strategy

### Docker-First Testing

**Why Docker**:
- Runs on all platforms (Linux CI, Mac dev, Windows if needed)
- Fast (seconds to provision)
- Reliable (no flaky VMs)
- Real implementation (not mocked)

**Test pyramid**:
```
     E2E (Docker)
    /            \
   Integration    \
  (Docker real)    \
 Unit (mocked)      \
```

### Hyper-V Stubbing

**Stub provider** for testing distributed architecture:
```go
type StubHyperVProvider struct {
    vms map[string]*stubVM
}

// Simulate realistic timing
func (s *StubHyperVProvider) Provision(ctx, spec) (*Resource, error) {
    time.Sleep(10 * time.Second) // Realistic provision time
    vm := &stubVM{state: "running"}
    return &Resource{...}, nil
}

// Simulate Execute() for hooks
func (s *StubHyperVProvider) Execute(ctx, res, cmd) (*ExecuteResult, error) {
    // Parse and simulate command execution
    return &ExecuteResult{ExitCode: 0, Stdout: "OK"}, nil
}
```

### Debug & Troubleshooting

**Verbose logging**:
```bash
boxy serve --log-level debug

# Shows:
# - Each provider call with timing
# - Hook execution with stdout/stderr
# - State transitions
# - Error details
```

**Manual testing**:
```bash
# Test finalization independently
boxy admin pool test-finalization my-pool --debug

# Test personalization independently
boxy admin resource test-personalization res-abc123 \
  --username testuser \
  --password testpass \
  --debug
```

## Performance Targets

**Pool replenishment**:
- Docker: <10 seconds
- Hyper-V (with differencing disk): <30 seconds
- Hyper-V (full copy): <3 minutes

**Allocation (personalization)**:
- Target: <10 seconds
- Max acceptable: 30 seconds
- If slower, user should optimize hooks or base image

**Timeouts** (configurable per pool):
- Provision: 300s default
- Finalization: 600s default
- Personalization: 30s default
- Destroy: 60s default

## Key Design Principles

1. **Provider is dumb**: Just translates, no logic
2. **Agent is proxy**: No autonomy, just remote execution
3. **Base images do heavy lifting**: Hooks for finalization only
4. **Vending machine model**: Take resource, it's yours until expiration
5. **Async by default**: Don't block, show progress
6. **Test with Docker**: Real implementation, cross-platform
7. **Stub what you can't test**: Hyper-V stub for non-Windows
8. **Debug-friendly**: Verbose logging, manual test commands
9. **Config over code**: Hooks in YAML, not hardcoded
10. **Simple first**: Start with working MVP, add features later

## Open Questions (Not Blocking MVP)

1. Max expiration limits? Unlimited extensions OK?
2. Should system hooks be pluggable/overridable?
3. Hook variable expansion - how rich should it be?
4. Should we track resource costs/usage?
5. Certificate rotation - manual or automatic?

## Summary

**MVP delivers**:
- Working pool management with Docker
- Hook-based customization
- Async allocation with good UX
- Foundation for distributed (token registration, stub Hyper-V)
- Comprehensive testing

**Ready to build**: All architectural decisions made, clear scope, test strategy defined.
