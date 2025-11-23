# ADR-002: Provider Architecture

**Date**: 2025-11-17
**Status**: Accepted

## Context

Boxy needs to support multiple backend providers (Docker, Hyper-V, KVM, VMware, etc.). We need to decide on the plugin/provider architecture.

## Decision

We will use **compiled-in providers** with a standardized interface, NOT Go's built-in plugin system or external plugin mechanisms.

### Provider Interface

```go
type Provider interface {
    // Provision creates a new resource based on the specification
    Provision(ctx context.Context, spec ResourceSpec) (*Resource, error)

    // Destroy removes a resource and cleans up all associated data
    Destroy(ctx context.Context, resourceID string) error

    // GetStatus returns the current status of a resource
    GetStatus(ctx context.Context, resourceID string) (*ResourceStatus, error)

    // GetConnectionInfo returns connection details for a resource
    GetConnectionInfo(ctx context.Context, resourceID string) (*ConnectionInfo, error)

    // HealthCheck verifies the provider is operational
    HealthCheck(ctx context.Context) error
}
```

### Provider Registration

```go
// All providers compiled into main binary
registry := provider.NewRegistry()
registry.Register("docker", docker.NewProvider())
registry.Register("hyperv", hyperv.NewProvider())
registry.Register("kvm", kvm.NewProvider())
```

## Rationale

### Why NOT Go's Plugin System?

Go's built-in `plugin` package has significant limitations:

- ❌ Requires exact Go version match between plugin and host
- ❌ Doesn't work on Windows
- ❌ Can't unload plugins
- ❌ Runtime errors instead of compile-time safety
- ❌ Difficult to debug

### Why Compiled-In Providers?

1. **Simplicity**: No runtime complexity, no process management
2. **Type Safety**: Compile-time interface verification
3. **Performance**: No IPC overhead
4. **Reliability**: No plugin loading failures
5. **Testing**: Easy to mock and test
6. **Distribution**: Single binary contains all providers
7. **Industry Standard**: Used by Docker CLI, kubectl, GitHub CLI

### Why NOT gRPC-based Plugins?

While HashiCorp's go-plugin (gRPC-based) is excellent, it adds complexity:

- Process management overhead
- IPC serialization costs
- More complex debugging
- Additional failure modes

**We don't need third-party extensibility for MVP** - we control all providers.

## Consequences

### Positive

- Simple implementation and testing
- No runtime plugin failures
- Compile-time type safety
- Single binary distribution
- Fast performance (no IPC)

### Negative

- Can't add providers without recompiling
- Larger binary size (all providers included)
- No third-party provider ecosystem (yet)

### Migration Path

If we need external plugins in the future (Phase 3+):

1. Keep existing interface unchanged
2. Add HashiCorp go-plugin wrapper
3. Support both compiled-in and external providers
4. Example: `boxy plugin install community/vmware`

This is a proven approach (Terraform supports both built-in and external providers).

## Alternatives Considered

### 1. HashiCorp go-plugin (gRPC)

**Rejected for MVP**: Adds complexity without clear benefit. Revisit if third-party providers are requested.

### 2. WebAssembly Plugins

**Rejected**: Immature ecosystem, limited system access, unnecessary complexity.

### 3. Scripting Language Plugins (Lua, JavaScript)

**Rejected**: Performance overhead, limited type safety, complex provider implementation.

## References

- [Tech Stack Research - Plugin Architecture](../architecture/tech-stack-research.md#pluginprovider-architecture-analysis)
- [CLAUDE.md - DRY Strategy](../CLAUDE.md#4-dry--dont-reinvent-the-wheel)
