# Technology Stack Research

## Executive Summary

**Recommendation**: Go + PostgreSQL + Redis + React

**Rationale**: Go provides excellent concurrency, strong ecosystem for system tools, and good virtualization SDKs. PostgreSQL offers robust state management. Redis enables efficient job queuing and pub/sub for real-time updates.

---

## Language Options Analysis

### Option 1: Go ⭐ RECOMMENDED

**Pros**:
- ✅ Excellent concurrency model (goroutines for pool management, background workers)
- ✅ Strong ecosystem for system tools and virtualization
  - Docker SDK: `github.com/docker/docker/client`
  - Libvirt bindings: `libvirt.org/go/libvirt`
  - VMware SDK available
- ✅ Single binary distribution (easy deployment)
- ✅ Great CLI libraries (Cobra, standard in cloud-native tools)
- ✅ Excellent HTTP frameworks (Gin, Echo, Chi)
- ✅ Strong typing and compile-time safety
- ✅ Fast compilation and execution
- ✅ Good database libraries (GORM, sqlx)
- ✅ Native support for plugins (though see skepticism below)

**Cons**:
- ⚠️ Go plugins are problematic (version matching, platform-specific)
- ⚠️ Verbose error handling
- ⚠️ No generics until recently (Go 1.18+)

**Key Libraries Available**:
```
github.com/spf13/cobra          # CLI framework
github.com/gin-gonic/gin        # HTTP framework
github.com/docker/docker        # Docker SDK
gorm.io/gorm                    # ORM
github.com/spf13/viper          # Configuration
github.com/sirupsen/logrus      # Logging
github.com/stretchr/testify     # Testing
github.com/go-redis/redis       # Redis client
```

**Verdict**: Strong choice for MVP and production

---

### Option 2: Rust

**Pros**:
- ✅ Maximum safety and performance
- ✅ No garbage collector (predictable latency)
- ✅ Strong type system prevents many bugs
- ✅ Growing ecosystem

**Cons**:
- ❌ Steeper learning curve
- ❌ Slower development velocity
- ❌ Less mature virtualization SDKs
- ❌ Smaller ecosystem for system management tools
- ⚠️ Compile times can be slow
- ⚠️ Async ecosystem still maturing (tokio)

**Verdict**: Great for performance-critical components, but overkill for MVP

---

### Option 3: Python

**Pros**:
- ✅ Rapid development
- ✅ Extensive libraries
- ✅ Great for prototyping
- ✅ Good Docker SDK (`docker-py`)
- ✅ Libvirt bindings available

**Cons**:
- ❌ GIL limits true concurrency (critical for pool management)
- ❌ Runtime dependency issues
- ❌ Slower execution (matters when managing many resources)
- ❌ Packaging and distribution more complex
- ⚠️ Type safety requires mypy/pyright

**Verdict**: Good for prototyping, but Go better for production system tool

---

### Option 4: TypeScript/Node.js

**Pros**:
- ✅ Great for Web UI backend
- ✅ Large ecosystem
- ✅ Good Docker SDK (`dockerode`)
- ✅ Unified language for frontend and backend

**Cons**:
- ❌ Single-threaded (worker threads exist but awkward)
- ❌ Less mature virtualization SDKs
- ❌ Runtime dependency management
- ❌ Not ideal for system-level tools
- ⚠️ Type safety not as strong as Go/Rust

**Verdict**: Consider for Web UI, but not for core service

---

## 🎯 Language Recommendation: **Go**

For a system tool that manages resources with complex concurrency needs, Go is the clear winner. It has the right balance of:
- Developer productivity
- Performance
- Ecosystem maturity
- Deployment simplicity

---

## Database Options Analysis

### Option 1: PostgreSQL ⭐ RECOMMENDED

**Pros**:
- ✅ Robust, battle-tested
- ✅ JSONB support (flexible schema for provider-specific data)
- ✅ Excellent ACID guarantees
- ✅ Great tooling and ecosystem
- ✅ Support for advisory locks (helpful for pool management)
- ✅ Full-text search capabilities
- ✅ Scalable to production

**Cons**:
- ⚠️ Requires separate process (but this is normal for production)
- ⚠️ More complex than SQLite for development

**Use Cases**:
- Store pool configurations
- Track sandbox state
- Record resource allocations
- Audit logging

**Verdict**: Best choice for production-grade state storage

---

### Option 2: SQLite

**Pros**:
- ✅ Zero configuration
- ✅ Single file
- ✅ Perfect for development and testing
- ✅ No separate process

**Cons**:
- ❌ Concurrent writes are limited
- ❌ Not suitable for distributed deployments
- ❌ Limited JSON support compared to PostgreSQL

**Verdict**: Great for MVP and local development, but plan PostgreSQL migration

---

### Option 3: Redis

**Pros**:
- ✅ Extremely fast
- ✅ Built-in pub/sub (great for events)
- ✅ Job queue support
- ✅ TTL support (auto-cleanup)

**Cons**:
- ❌ Primarily in-memory (persistence options exist but not primary use case)
- ❌ Not suitable as primary state store
- ❌ Limited query capabilities

**Verdict**: Use as complement to PostgreSQL (caching, job queue, pub/sub)

---

## 🎯 Database Recommendation: **PostgreSQL (primary) + Redis (secondary)**

**Strategy**:
- **PostgreSQL**: Authoritative state storage
- **Redis**: Job queue, caching, pub/sub for real-time updates
- **SQLite**: Development/testing (via same ORM, easy to swap)

---

## Plugin/Provider Architecture Analysis

### 🚨 SKEPTICISM ALERT: Go's Built-in Plugin System

Go has a `plugin` package, but it's **problematic**:

**Issues**:
- ❌ Requires exact same Go version for plugin and host
- ❌ Platform-specific (doesn't work on Windows)
- ❌ Can't unload plugins
- ❌ Difficult debugging
- ❌ Runtime errors instead of compile-time safety

**Recommendation**: **DON'T use Go plugins**

---

### Option 1: Interface-based Providers ⭐ RECOMMENDED (MVP)

Compile all providers into the main binary:

```go
type Provider interface {
    Provision(spec ResourceSpec) (*Resource, error)
    Destroy(id string) error
    GetStatus(id string) (*Status, error)
    GetConnectionInfo(id string) (*ConnectionInfo, error)
}

// Implementations
type DockerProvider struct{}
type HyperVProvider struct{}
type KVMProvider struct{}
```

**Pros**:
- ✅ Simple, no runtime complexity
- ✅ Compile-time type safety
- ✅ Easy to test
- ✅ No versioning issues

**Cons**:
- ⚠️ All providers must be compiled in (binary size)
- ⚠️ Can't add providers without recompilation

**Verdict**: Perfect for MVP, revisit for Phase 2+

---

### Option 2: gRPC-based Plugins

Providers run as separate processes, communicate via gRPC:

```protobuf
service ProviderService {
  rpc Provision(ResourceSpec) returns (Resource);
  rpc Destroy(ResourceID) returns (Empty);
  rpc GetStatus(ResourceID) returns (Status);
}
```

**Pros**:
- ✅ Language-agnostic (providers can be written in anything)
- ✅ Process isolation
- ✅ Can add providers without recompilation
- ✅ Hashicorp uses this approach (Terraform, Vault, etc.)

**Cons**:
- ⚠️ More complex (process management, RPC overhead)
- ⚠️ Harder to debug
- ⚠️ More moving parts

**Verdict**: Consider for Phase 2+ if third-party providers needed

---

### Option 3: HashiCorp go-plugin

Uses hashicorp's plugin framework (gRPC-based):

```go
import "github.com/hashicorp/go-plugin"
```

**Pros**:
- ✅ Production-tested (used by Terraform, Vault, Nomad)
- ✅ Handles process management
- ✅ gRPC-based
- ✅ Well-documented

**Cons**:
- ⚠️ Still complex for MVP
- ⚠️ External dependency

**Verdict**: Best option if we need external plugins (Phase 2+)

---

## 🎯 Plugin Recommendation

**Phase 1 (MVP)**: Interface-based (compiled-in)
**Phase 2+**: Consider HashiCorp go-plugin if extensibility needed

---

## Configuration Format

### Options: YAML vs TOML vs JSON

**YAML** ⭐ RECOMMENDED
```yaml
pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 3
    max_total: 10
```

**Pros**:
- ✅ Human-readable
- ✅ Supports comments
- ✅ Common in cloud-native tools (Kubernetes, Docker Compose)
- ✅ Go library: `gopkg.in/yaml.v3`

**Cons**:
- ⚠️ Whitespace-sensitive

**Verdict**: Standard choice for configuration

---

## DRY Strategy: Package Research

### Core Dependencies for Go MVP

#### 1. CLI Framework
**Package**: `github.com/spf13/cobra`
- Used by: kubectl, gh, docker, hugo
- Comprehensive, well-documented
- Subcommand support
- Flag handling
- Auto-generated help

**Alternative**: `github.com/urfave/cli` (simpler, less features)

**Verdict**: Cobra is industry standard

---

#### 2. Configuration Management
**Package**: `github.com/spf13/viper`
- Config file support (YAML, TOML, JSON)
- Environment variable binding
- Remote config (optional)
- Works seamlessly with Cobra

**Verdict**: De facto standard for Go CLI tools

---

#### 3. HTTP Framework
**Package**: `github.com/gin-gonic/gin`
- Fast, minimal overhead
- Middleware support
- Good JSON handling
- Well-documented

**Alternative**: `github.com/labstack/echo` (similar), `net/http` (stdlib, more verbose)

**Verdict**: Gin is excellent choice

---

#### 4. ORM/Database
**Package**: `gorm.io/gorm` v2
- Supports PostgreSQL, SQLite, MySQL
- Migration support
- Relationship handling
- Good documentation

**Alternative**: `github.com/jmoiron/sqlx` (lighter, more control)

**Verdict**: GORM for rapid development, consider sqlx for performance-critical parts

---

#### 5. Docker SDK
**Package**: `github.com/docker/docker/client`
- Official Docker client for Go
- Comprehensive API coverage
- Well-maintained

**Verdict**: Only real option, it's official

---

#### 6. Logging
**Package**: `github.com/sirupsen/logrus`
- Structured logging
- Multiple output formats (JSON, text)
- Hook system
- Good performance

**Alternative**: `go.uber.org/zap` (faster, more complex)

**Verdict**: Logrus for MVP, Zap if performance critical

---

#### 7. Testing
**Package**: `github.com/stretchr/testify`
- Assert and require helpers
- Mock support
- Suite support

**Alternative**: Standard `testing` package (more verbose)

**Verdict**: Testify reduces boilerplate significantly

---

#### 8. Redis Client
**Package**: `github.com/redis/go-redis/v9`
- Official Redis client
- Supports Redis 6+ features
- Connection pooling

**Verdict**: Standard choice

---

#### 9. Validation
**Package**: `github.com/go-playground/validator/v10`
- Struct validation via tags
- Custom validators
- Extensive rule library

**Verdict**: Industry standard for validation

---

## What NOT to Build (DRY Violations to Avoid)

❌ **Don't build**:
- HTTP server (use Gin/Echo)
- ORM (use GORM)
- CLI framework (use Cobra)
- Docker client (use official SDK)
- Logging library (use logrus/zap)
- Configuration parser (use Viper)
- UUID generation (use `github.com/google/uuid`)
- Password hashing (use `golang.org/x/crypto/bcrypt`)
- JWT handling (use `github.com/golang-jwt/jwt`)

✅ **Do build**:
- Pool management logic
- Sandbox orchestration
- Provider interface and implementations
- Resource lifecycle management
- Expiration/cleanup scheduling
- Boxy-specific business logic

---

## MVP Technology Stack (Final Recommendation)

### Core
- **Language**: Go 1.21+
- **Database**: PostgreSQL 15+ (SQLite for dev)
- **Cache/Queue**: Redis 7+

### Key Libraries
```
# CLI & Config
github.com/spf13/cobra          v1.8+
github.com/spf13/viper          v1.18+

# HTTP (Phase 3)
github.com/gin-gonic/gin        v1.10+

# Database
gorm.io/gorm                    v1.25+
gorm.io/driver/postgres         v1.5+
gorm.io/driver/sqlite           v1.5+

# Docker
github.com/docker/docker        v24.0+

# Utilities
github.com/google/uuid          v1.5+
github.com/sirupsen/logrus      v1.9+
github.com/go-playground/validator/v10  v10.16+

# Testing
github.com/stretchr/testify     v1.8+
```

### Development Tools
- **Testing**: Go test + testify
- **Linting**: golangci-lint
- **Formatting**: gofmt, goimports
- **Build**: Make or Task
- **CI/CD**: GitHub Actions

---

## Open Questions & Skepticism

### 🤔 Question 1: Warm Pools in MVP?

**Concern**: Maintaining warm pools (pre-provisioned containers) adds significant complexity:
- Background workers to maintain min_ready count
- Health checking provisioned resources
- Startup/shutdown edge cases

**Proposal**: For MVP, what if we do **on-demand provisioning only**?
- Simpler to implement and test
- Still demonstrates core value
- Add warm pools in Phase 2

**Your input needed**: Is on-demand okay for MVP, or is warm pool essential to the value prop?

---

### 🤔 Question 2: State Storage in MVP

**Concern**: Setting up PostgreSQL adds deployment complexity for early testing/demos.

**Proposal**:
- Use SQLite for Phase 1 (single file, zero config)
- Design with ORM so PostgreSQL swap is trivial (GORM supports both)
- Migrate to PostgreSQL in Phase 3 (when we add API server)

**Your input needed**: Is this pragmatic, or should we start with PostgreSQL from day 1?

---

### 🤔 Question 3: Configuration vs Database

**Concern**: Should pool definitions live in:
- **Config files** (YAML) - Simple, version-controllable
- **Database** - Dynamic, API-manageable

**Proposal**:
- Phase 1: Config files (simpler)
- Phase 3: Migrate to database (when API added)

**Your input needed**: Acceptable, or do we need database-backed pools from start?

---

### 🤔 Question 4: Credential Storage

**Concern**: Where/how do we store generated credentials?
- In database (encrypted at rest)
- In memory only (lost on restart, but more secure)
- External secrets manager (HashiCorp Vault)

**Proposal**:
- Phase 1: In-memory only (ephemeral sandboxes)
- Phase 2+: Database encrypted with at-rest encryption
- Phase 3+: Optional Vault integration

**Your input needed**: Is in-memory acceptable for MVP?

---

## Next Steps

1. **Get your feedback** on open questions above
2. Create ADRs documenting final decisions
3. Set up Go project structure
4. Begin implementation

---

## Confidence Levels

- **Go as language**: 95% confident (excellent fit)
- **PostgreSQL for production**: 95% confident
- **SQLite for MVP**: 85% confident (tradeoff for simplicity)
- **Interface-based providers**: 90% confident for MVP
- **Warm pools in MVP**: 60% confident (adds complexity, maybe defer?)
- **Config file vs DB**: 70% confident (config simpler for MVP)

**Ready for your input on the skeptical questions above!**
