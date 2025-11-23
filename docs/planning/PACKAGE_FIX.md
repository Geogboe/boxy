# Package Organization Fix

## Current Problem

**The dependency graph is backwards:**

```text
pkg/provider/provider.go
  └─> imports internal/core/resource  ❌ ILLEGAL

internal/provider/docker/
internal/provider/hyperv/
  └─> implements pkg/provider.Provider
```

**Go rule**: `pkg/` CANNOT import from `internal/`. This is enforced by the compiler.

## Root Cause

The `resource.Resource` types are in `internal/core/resource`, but they're actually part of the **provider contract**, not internal orchestration.

## The Fix

### Move Resource Types to pkg/provider

```text
pkg/provider/
├── provider.go         # Provider interface
├── types.go            # Resource, ResourceSpec, ResourceStatus, ConnectionInfo
├── docker/
│   └── provider.go     # Implements provider.Provider
├── hyperv/
│   └── provider.go     # Implements provider.Provider
└── mock/
    └── mock.go         # Mock for testing
```

### What Stays in internal/

```text
internal/core/
├── pool/               # Pool orchestration (uses provider.Provider)
├── sandbox/            # Sandbox orchestration (uses provider.Provider)
└── allocator/          # Allocation logic (uses provider.Provider)
```

## Corrected Dependency Graph

```text
internal/core/pool/
  └─> imports pkg/provider  ✅ CORRECT

pkg/provider/docker/
  └─> imports pkg/provider  ✅ CORRECT (types in same pkg)

pkg/provider/hyperv/
  └─> imports pkg/hyperv    ✅ CORRECT
  └─> imports pkg/provider  ✅ CORRECT
```

## Why This Makes Sense

### Provider Package Should Contain

- ✅ **Provider interface** - Contract all providers implement
- ✅ **Resource types** - What providers create/manage
- ✅ **Provider implementations** - Docker, Hyper-V, etc.
- ✅ **Mock provider** - For testing consumers

### Internal Should Contain

- ✅ **Pool orchestration** - Uses providers abstractly
- ✅ **Sandbox orchestration** - Uses providers abstractly
- ✅ **Allocation logic** - Coordinates pool/sandbox
- ✅ **Storage** - Persists provider resources
- ✅ **API server** - HTTP/gRPC endpoints

## Migration Steps

### 1. Move resource types to pkg/provider/types.go

```go
// pkg/provider/types.go
package provider

// Resource represents a provisioned compute resource
type Resource struct {
    ID           string
    Type         ResourceType
    State        ResourceState
    ProviderType string
    ProviderID   string
    Spec         map[string]interface{}
    Metadata     map[string]interface{}
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

// ResourceSpec defines what to provision
type ResourceSpec struct {
    Type        ResourceType
    Image       string
    CPUs        int
    MemoryMB    int
    DiskGB      int
    Labels      map[string]string
    Environment map[string]string
}

// ResourceStatus represents current resource status
type ResourceStatus struct {
    State     ResourceState
    Healthy   bool
    Message   string
    LastCheck time.Time
}

// ConnectionInfo provides access credentials
type ConnectionInfo struct {
    Type        string
    Host        string
    Port        int
    Username    string
    Password    string
    ExtraFields map[string]interface{}
}

// ResourceType enum
type ResourceType string

const (
    ResourceTypeVM        ResourceType = "vm"
    ResourceTypeContainer ResourceType = "container"
    ResourceTypeProcess   ResourceType = "process"
)

// ResourceState enum
type ResourceState string

const (
    StateProvisioning ResourceState = "provisioning"
    StateReady        ResourceState = "ready"
    StateAllocated    ResourceState = "allocated"
    StateError        ResourceState = "error"
    StateDestroyed    ResourceState = "destroyed"
)
```

### 2. Update provider interface

```go
// pkg/provider/provider.go
package provider

import "context"

type Provider interface {
    Provision(ctx context.Context, spec ResourceSpec) (*Resource, error)
    Destroy(ctx context.Context, res *Resource) error
    GetStatus(ctx context.Context, res *Resource) (*ResourceStatus, error)
    GetConnectionInfo(ctx context.Context, res *Resource) (*ConnectionInfo, error)
    Update(ctx context.Context, res *Resource, updates ResourceUpdate) error
    Exec(ctx context.Context, res *Resource, cmd []string) (*ExecResult, error)
    HealthCheck(ctx context.Context) error
    Name() string
    Type() ResourceType
}
```

### 3. Move provider implementations

```text
internal/provider/docker/  → pkg/provider/docker/
internal/provider/hyperv/  → pkg/provider/hyperv/
internal/provider/mock/    → pkg/provider/mock/
```

### 4. Update imports across codebase

```go
// Before
import "github.com/Geogboe/boxy/internal/core/resource"
import "github.com/Geogboe/boxy/internal/provider/docker"

// After
import "github.com/Geogboe/boxy/pkg/provider"
import "github.com/Geogboe/boxy/pkg/provider/docker"
```

### 5. Delete internal/core/resource

Once all migrations complete, this package should be empty and can be removed.

## Agent Package

**Also move** `internal/agent/` → `pkg/agent/`:

```text
pkg/agent/
├── agent.go           # Agent client (connects to server)
├── server.go          # Server-side agent management
├── README.md          # How to deploy agents
└── proto/             # gRPC protocol (if not in pkg/api)
```

**Why?**

- Reusable framework for distributed provider execution
- Clear gRPC contract
- Could be used by other projects
- No Boxy-specific orchestration logic

## Summary

**What's Moving to pkg/:**

1. ✅ Provider types (Resource, ResourceSpec, etc.)
2. ✅ Provider implementations (docker, hyperv, mock)
3. ✅ Agent framework

**What Stays in internal/:**

1. ✅ Pool management (orchestration)
2. ✅ Sandbox management (orchestration)
3. ✅ Allocator (orchestration)
4. ✅ Storage (data persistence)
5. ✅ Server (API endpoints)
6. ✅ Hooks (lifecycle glue code)

**Result:**

- Clean dependency graph (no pkg/ → internal/)
- Reusable provider framework
- Smaller context windows per component
- Clear contracts at each layer
