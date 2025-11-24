# ADR-007: Scratch Provider Architecture

**Date**: 2025-11-24
**Status**: Proposed
**Supersedes**: ADR-006 (Runner Provider)

## Context

Boxy currently supports VM and container providers (Docker, Hyper-V, KVM, VMware) that manage heavyweight isolation. We need a lightweight option for quick local scratch spaces that:

1. Provides a simple entry point for users new to Boxy
2. Offers instant allocation for temporary workspaces
3. Maintains consistency with the existing provider model
4. Handles automatic cleanup like other providers

The challenge is that scratch spaces are fundamentally different from VMs/containers:

- **Passive resources**: Just directories on disk, nothing actively "running"
- **Instant provisioning**: No boot time, `mkdir` is ~instantaneous
- **Different isolation**: File system boundaries rather than process/network isolation
- **Simpler lifecycle**: No complex start/stop/restart semantics

We need to determine:

1. How scratch fits into the provider model
2. Whether we need a "keeper process" to health check
3. How preheating works for instant resources
4. What the provider hierarchy should look like

## Decision

### Provider Hierarchy

We will create a **`scratch/`** provider family for lightweight local isolation:

```text
pkg/provider/
├── docker/
├── hyperv/
├── scratch/
│   ├── shell/       # Configurable shell environments (bash/zsh/fish)
│   ├── venv/        # Python virtual environments (future)
│   └── tmux/        # Persistent tmux sessions (future)
```

The `scratch/` namespace clearly distinguishes lightweight local resources from heavyweight VM/container isolation.

### No Keeper Process

We will **NOT** use a dummy "keeper process" (like `sleep infinity`) for scratch resources.

**Rationale:**

- Sandbox expiration is handled by the Sandbox Manager, not by process lifecycle
- Health checking can validate directory state directly
- A keeper process adds complexity without providing value
- The filesystem directory *is* the resource - nothing needs to "run"

### Simulated Preheating

Scratch providers will **simulate preheating** to maintain interface consistency:

**Provision (preheating):**

- Create workspace directory structure
- Write resource metadata file (`.boxy-resource`)
- Mark resource as READY
- Time cost: ~instant (just `mkdir`)

**Allocate (assignment to sandbox):**

- Generate connect script with sandbox-specific info
- Create shell environment configuration
- Write sandbox metadata (`.boxy-sandbox`)
- Set proper permissions

**Why this works:**

- Satisfies the provider contract (provision → allocate flow)
- Keeps the Provider interface simple and consistent
- `min_ready` still has meaning (pre-created directories ready to allocate)
- Fast enough that preheating cost is negligible

### Health Check Without Process

Health checks will validate **filesystem state only**:

```go
func (p *ShellScratchProvider) HealthCheck(ctx context.Context, resourceID string) error {
    // 1. Check workspace directory exists
    // 2. Verify directory is readable/writable
    // 3. Check available disk space meets minimum
    // 4. Validate metadata files are intact
    return nil
}
```

This doubles as reconciliation - if the directory is gone, health check fails, and the pool manager handles cleanup.

### Resource Structure

**Provisioned (READY) state:**

```text
/tmp/boxy/res_abc123/
├── .boxy-resource      # Resource metadata (ID, pool, timestamp)
└── workspace/          # Empty workspace directory
```

**Allocated (ALLOCATED) state:**

```text
/tmp/boxy/res_abc123/
├── .boxy-resource      # Resource metadata
├── .boxy-sandbox       # Sandbox metadata (ID, expiration)
├── connect.sh          # Connection script (executable)
├── .envrc              # Shell environment configuration
└── workspace/          # User's working directory
    └── (user files)
```

### Connection Model

The `connect.sh` script provides access to the sandbox:

```bash
#!/bin/bash
# Generated during allocation with sandbox-specific info

cd /tmp/boxy/res_abc123/workspace || exit 1
export BOXY_SANDBOX=sbox_xyz
export BOXY_WORKSPACE=/tmp/boxy/res_abc123/workspace
export PATH=/limited/path
export PS1="(boxy:sbox_xyz) \\w $ "

exec bash --norc --noprofile
```

Users simply run the script to enter the isolated environment. Each invocation spawns a fresh shell. Multiple concurrent connections are supported.

### Lifecycle Management

All lifecycle operations remain server-side:

1. **Provision**: Server creates directory structure
2. **Health Check**: Server validates filesystem state periodically
3. **Allocate**: Server generates connection artifacts
4. **Destroy**: Server removes directory tree
5. **Reconcile**: Server checks directories on startup, cleans up orphans

The client script is completely passive - it has no lifecycle responsibilities.

## Consequences

### Positive

✅ **Consistent provider model**: Scratch fits the same provision/allocate/destroy pattern
✅ **Simple implementation**: No process management, just filesystem operations
✅ **Fast allocation**: Pre-created directories enable instant assignment
✅ **Clean interface**: No special cases in Provider interface
✅ **Easy debugging**: Directory state is inspectable with standard tools
✅ **Reconciliation works**: Startup can scan filesystem to sync DB state
✅ **Extensible**: Easy to add more scratch variants (venv, tmux, etc.)

### Negative

❌ **"Preheating" is somewhat artificial**: The work is trivial, so preheating provides minimal benefit
❌ **No resource enforcement**: Can't limit CPU/memory without additional tooling (cgroups)
❌ **Local only**: Scratch resources can't be accessed remotely (by design)
❌ **Simpler isolation**: File system boundaries only, no network/process isolation

### Neutral

- Server restart loses track of scratch resources (reconciliation cleans them up)
- Scratch spaces are explicitly ephemeral and non-persistent
- Users must understand scratch is different from VM/container isolation

## Implementation Notes

### Phase 1: `scratch/shell` Provider (registry key: "scratch/shell")

Start with the simplest variant:

- Configurable shell type (bash/zsh/fish detection)
- Basic PATH restriction
- Environment variable isolation
- Auto-generated connect script

### Future Variants

- `scratch/venv`: Python virtual environments with package isolation
- `scratch/tmux`: Persistent tmux sessions (actual long-running process)
- `scratch/conda`: Conda environments
- `scratch/nix`: Nix shell environments

### Shared helper package

- `pkg/workspacefs`: filesystem helpers for scratch-class providers (layout, metadata files, connect script generation, health checks, cleanup).

### Configuration Example

```yaml
pools:
  - name: quick-scratch
    type: process
    backend: scratch/shell
    min_ready: 10        # Keep 10 directories pre-created
    max_total: 100
    workspace_size_mb: 500
    allowed_shells:
      - bash
      - zsh
    health_check_interval: 60s
```

## Alternatives Considered

### Alternative 1: Keeper Process with Health Checking

Use a `sleep infinity` process to have something to health check.

**Rejected because:**

- Adds unnecessary complexity
- Process lifecycle doesn't map to resource lifecycle
- Sandbox manager already handles expiration
- Health checking directory state is simpler and sufficient

### Alternative 2: No Preheating (Provision on Demand)

Set `min_ready: 0` and create directories only when sandboxes are requested.

**Rejected because:**

- Breaks consistency with other providers
- Removes a useful abstraction (even if performance gain is minimal)
- Makes pool statistics less meaningful
- Harder to reason about resource availability

### Alternative 3: Special "Local" Provider Category

Create a separate provider type/interface for local resources.

**Rejected because:**

- Fragments the provider model
- Requires special handling throughout codebase
- Provider interface is already flexible enough
- "Scratch" namespace is clearer than "local"

## References

- [ADR-002: Provider Architecture](adr-002-provider-architecture.md)
- [docs/USE_CASES.md](../USE_CASES.md) - Quick testing environment use case
- Provider interface: `pkg/provider/provider.go`
- Pool manager: `internal/core/pool/manager.go`

## Related Decisions

- ADR-002 defined the provider architecture that scratch extends
- Future ADR needed for resource limit enforcement (cgroups, ulimits)
- Future ADR needed for remote scratch access (if desired)
