# ADR-001: Technology Stack

**Date**: 2025-11-17
**Status**: Accepted

## Context

Boxy requires a technology stack that supports:
- Complex concurrency (warm pool maintenance, background workers)
- System-level resource management (Docker, VMs)
- Single binary distribution for easy deployment
- Strong ecosystem for virtualization APIs

## Decision

We will use **Go** as the primary language with the following supporting technologies:

### Core Stack
- **Language**: Go 1.21+
- **Database**: SQLite (MVP), PostgreSQL (Production)
- **Cache/Queue**: Redis (Phase 3+)

### Key Libraries
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `gorm.io/gorm` - ORM (supports both SQLite and PostgreSQL)
- `github.com/docker/docker` - Docker SDK
- `github.com/sirupsen/logrus` - Structured logging
- `github.com/stretchr/testify` - Testing utilities
- `github.com/google/uuid` - UUID generation

## Rationale

### Why Go?

1. **Excellent Concurrency**: Goroutines are perfect for warm pool management where each pool needs continuous background monitoring and replenishment.

2. **Strong Ecosystem**:
   - Official Docker SDK
   - Mature libvirt bindings for KVM
   - VMware SDKs available

3. **Single Binary Distribution**: Simplifies deployment - no runtime dependencies.

4. **System Tool Heritage**: Go is the standard for cloud-native system tools (Docker, Kubernetes, Terraform).

5. **Type Safety**: Compile-time checks prevent entire classes of bugs.

### Why Not Alternatives?

**Python**:
- GIL limits true concurrency (critical for warm pools)
- Runtime dependency management complexity
- Slower performance for resource-intensive operations

**Rust**:
- Slower development velocity
- Less mature virtualization ecosystem
- Overkill for this problem domain

**TypeScript/Node.js**:
- Single-threaded (worker threads are awkward)
- Not ideal for system-level tools
- Less mature virtualization SDKs

### Why SQLite for MVP?

- Zero configuration (single file)
- Perfect for development and testing
- Easy to demo
- GORM allows trivial migration to PostgreSQL later

### Why Not PostgreSQL for MVP?

- Adds deployment complexity
- Overkill for single-node MVP
- Can migrate seamlessly via GORM when needed (Phase 3)

## Consequences

### Positive
- Fast, reliable concurrency for warm pools
- Strong type safety reduces bugs
- Easy distribution (single binary)
- Mature virtualization SDKs available
- Excellent tooling and IDE support

### Negative
- More verbose than dynamic languages
- Smaller talent pool than JavaScript/Python
- Generic support only in recent versions (Go 1.18+)

### Neutral
- Need to use database abstraction (GORM) to keep PostgreSQL migration path open
- No runtime plugin system (use compiled-in providers)

## References
- [CLAUDE.md - Technology Stack Recommendations](../CLAUDE.md#7-technology-stack-recommendations)
- [Tech Stack Research](../architecture/tech-stack-research.md)
