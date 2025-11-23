# Package Restructure Plan

**Goal**: Separate reusable components into `/pkg` with clear contracts, enabling:

- Smaller context windows for focused work
- Independent testing and development
- Clear separation of concerns
- Potential reusability by other projects

**Philosophy**: Move fast, pre-release means we can refactor aggressively. Each package gets a README with contract, tests, and architecture links.

---

## Current Structure (Simplified)

```text
boxy/
├── cmd/
│   └── boxy/              # CLI tool
├── internal/
│   ├── agent/             # Distributed agent (v2 feature)
│   ├── config/            # Configuration management
│   ├── core/
│   │   ├── pool/          # Pool management
│   │   ├── resource/      # Resource types
│   │   └── sandbox/       # Sandbox orchestration
│   ├── crypto/            # Encryption
│   ├── hooks/             # Hook execution
│   ├── provider/
│   │   ├── docker/        # Docker implementation
│   │   ├── hyperv/        # Hyper-V implementation
│   │   └── mock/          # Mock for testing
│   └── storage/           # State persistence
└── pkg/
    ├── api/               # gRPC definitions (partial)
    ├── crypto/            # Started extraction
    └── provider/          # Started extraction
        ├── proto/         # gRPC proto
        └── remote/        # Remote provider (v2)
```

---

## Target Structure

```text
boxy/
├── cmd/
│   └── boxy/                      # CLI tool
│       ├── commands/              # CLI commands
│       └── main.go
│
├── pkg/                           # PUBLIC, REUSABLE COMPONENTS
│   │
│   ├── powershell/                # LOW-LEVEL: PowerShell execution from Go
│   │   ├── exec.go                # Execute PS scripts, marshal args/results
│   │   ├── types.go               # Result, Error types
│   │   ├── exec_test.go           # Unit tests (mock PS responses)
│   │   └── README.md              # Contract: Execute PS, get structured output
│   │
│   ├── hyperv/                    # MID-LEVEL: Hyper-V operations
│   │   ├── client.go              # Main Hyper-V client struct
│   │   ├── vm.go                  # VM lifecycle (New-VM, Start, Stop, Remove)
│   │   ├── vhd.go                 # VHD operations (New-VHD, Mount, etc.)
│   │   ├── checkpoint.go          # Snapshot management
│   │   ├── network.go             # Network adapter configuration
│   │   ├── types.go               # Hyper-V specific types (VM, VHD, etc.)
│   │   ├── errors.go              # Hyper-V specific errors
│   │   ├── client_test.go         # Tests (uses pkg/powershell mocks)
│   │   ├── README.md              # Contract: Hyper-V automation library
│   │   │
│   │   └── psdirect/              # SUBPACKAGE: PowerShell Direct
│   │       ├── psdirect.go        # Invoke-Command -VMName (exec inside VM)
│   │       ├── session.go         # Persistent PS sessions to VMs
│   │       ├── psdirect_test.go   # Tests
│   │       └── README.md          # Contract: Execute commands inside VMs
│   │
│   ├── docker/                    # MID-LEVEL: Docker operations
│   │   ├── client.go              # Docker client wrapper
│   │   ├── container.go           # Container lifecycle
│   │   ├── image.go               # Image management
│   │   ├── types.go               # Docker specific types
│   │   ├── client_test.go         # Tests
│   │   └── README.md              # Contract: Docker automation library
│   │
│   ├── crypto/                    # Encryption & credential management
│   │   ├── encryptor.go           # Encryption interface
│   │   ├── aes.go                 # AES implementation
│   │   ├── password.go            # Secure password generation (crypto/rand)
│   │   ├── crypto_test.go         # Tests
│   │   └── README.md              # Contract: Encrypt data, generate credentials
│   │
│   ├── hooks/                     # Hook execution system
│   │   ├── executor.go            # Execute hooks with timeout/error handling
│   │   ├── templates.go           # Template variable substitution
│   │   ├── types.go               # Hook, HookResult types
│   │   ├── executor_test.go       # Tests
│   │   └── README.md              # Contract: Execute lifecycle hooks
│   │
│   ├── provider/                  # Provider interface & implementations
│   │   ├── provider.go            # Core Provider interface
│   │   ├── types.go               # ResourceSpec, Resource, ConnectionInfo, etc.
│   │   ├── errors.go              # Provider-specific errors
│   │   ├── README.md              # Contract: Provider interface specification
│   │   │
│   │   ├── hyperv/                # HIGH-LEVEL: Hyper-V provider
│   │   │   ├── provider.go        # Implements provider.Provider
│   │   │   ├── provision.go       # Provision logic (uses pkg/hyperv)
│   │   │   ├── destroy.go         # Destroy logic
│   │   │   ├── hooks.go           # Hook execution (uses pkg/hyperv/psdirect)
│   │   │   ├── validation.go      # Base image validation
│   │   │   ├── provider_test.go   # Tests (can use pkg/hyperv mocks)
│   │   │   └── README.md          # Implements Provider for Hyper-V
│   │   │
│   │   ├── docker/                # HIGH-LEVEL: Docker provider
│   │   │   ├── provider.go        # Implements provider.Provider
│   │   │   ├── provision.go       # Provision logic (uses pkg/docker)
│   │   │   ├── destroy.go         # Destroy logic
│   │   │   ├── hooks.go           # Hook execution
│   │   │   ├── provider_test.go   # Tests
│   │   │   └── README.md          # Implements Provider for Docker
│   │   │
│   │   └── mock/                  # Mock provider for testing
│   │       ├── mock.go            # Simple mock implementation
│   │       └── README.md          # Mock provider for tests
│   │
│   └── client/                    # SDK for programmatic Boxy access
│       ├── client.go              # HTTP/gRPC client to Boxy API
│       ├── sandbox.go             # Sandbox operations
│       ├── pool.go                # Pool operations
│       ├── types.go               # Client-side types
│       ├── client_test.go         # Tests
│       └── README.md              # Contract: Boxy API client SDK
│
└── internal/                      # PRIVATE, BOXY-SPECIFIC ORCHESTRATION
    │
    ├── core/                      # Core orchestration logic
    │   ├── allocator/             # Resource allocation orchestrator (v1)
    │   │   ├── allocator.go       # Coordinates Pool → Sandbox movement
    │   │   ├── allocator_test.go
    │   │   └── types.go
    │   │
    │   ├── pool/                  # Pool management
    │   │   ├── manager.go         # Pool lifecycle, replenishment
    │   │   ├── preheating.go      # Preheating logic (v1)
    │   │   ├── recycling.go       # Resource recycling (v1)
    │   │   ├── types.go           # Pool, PoolConfig types
    │   │   ├── errors.go
    │   │   └── manager_test.go
    │   │
    │   ├── sandbox/               # Sandbox orchestration
    │   │   ├── manager.go         # Sandbox lifecycle, expiration
    │   │   ├── types.go           # Sandbox, SandboxConfig types
    │   │   ├── errors.go
    │   │   └── manager_test.go
    │   │
    │   └── scheduler/             # Background job scheduler (v1)
    │       ├── scheduler.go       # Expiration, recycling jobs
    │       └── scheduler_test.go
    │
    ├── storage/                   # State persistence layer
    │   ├── repository.go          # Data access interface
    │   ├── postgres.go            # PostgreSQL implementation
    │   ├── models.go              # DB models (Pool, Sandbox, Resource)
    │   └── migrations/            # SQL migrations
    │
    ├── server/                    # HTTP/gRPC API server
    │   ├── server.go              # Server initialization
    │   ├── handlers/              # API handlers
    │   │   ├── sandbox.go         # Sandbox endpoints
    │   │   ├── pool.go            # Pool endpoints
    │   │   └── health.go          # Health checks
    │   ├── middleware/            # HTTP middleware
    │   │   ├── auth.go            # Authentication (v1: API tokens)
    │   │   ├── logging.go         # Request logging
    │   │   └── errors.go          # Error handling
    │   └── grpc/                  # gRPC services (v2: distributed agents)
    │
    ├── config/                    # Configuration management
    │   ├── config.go              # Load/parse YAML config
    │   ├── validation.go          # Config validation
    │   └── defaults.go            # Default values
    │
    └── agent/                     # Distributed agent (v2 feature - keep internal for now)
        ├── server.go              # Agent gRPC server
        └── convert.go             # Type conversions

```

---

## Key Organizational Principles

### 1. **Layered Architecture**

```text
┌─────────────────────────────────────────────────────────────┐
│                    USER INTERFACES                           │
│                  cmd/boxy (CLI tool)                         │
└─────────────────────────────────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────────┐
│              BOXY ORCHESTRATION (internal/)                  │
│   Allocator, Pool Manager, Sandbox Manager, Storage, API    │
└─────────────────────────────────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────────┐
│           PROVIDER LAYER (pkg/provider/)                     │
│      Implements provider.Provider interface                  │
│        hyperv/, docker/, mock/                               │
└─────────────────────────────────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────────┐
│      PLATFORM LIBRARIES (pkg/hyperv/, pkg/docker/)           │
│         Platform-specific operations                         │
└─────────────────────────────────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────────┐
│      FOUNDATION UTILITIES (pkg/powershell/, pkg/crypto/)     │
│           Low-level, highly reusable                         │
└─────────────────────────────────────────────────────────────┘
```

### 2. **Package Dependency Rules**

**pkg/ packages:**

- ✅ Can depend on other pkg/ packages
- ❌ CANNOT depend on internal/ (enforced by Go)
- ✅ Should have minimal external dependencies
- ✅ Must have clear, stable interfaces

**internal/ packages:**

- ✅ Can depend on pkg/ packages
- ✅ Can depend on other internal/ packages
- ✅ Contain Boxy-specific business logic
- ✅ Can change frequently

**Example valid imports:**

```text
pkg/provider/hyperv → pkg/hyperv, pkg/hyperv/psdirect, pkg/hooks
pkg/hyperv → pkg/powershell
internal/core/pool → pkg/provider, pkg/hooks, pkg/crypto
internal/storage → pkg/provider (for types)
```

### 3. **Subpackage Usage**

**When to use subpackages:**

- Tightly coupled but conceptually distinct (e.g., `pkg/hyperv/psdirect`)
- Depends on parent package capabilities
- Significant enough to warrant separation (> 200 lines)
- Has its own clear contract

**When NOT to use:**

- Just for file organization (use multiple .go files in same package)
- Would create circular dependencies
- Too small to justify the complexity

---

## Package README Template

Every `/pkg` package should have a README with:

```markdown
# Package Name

## Purpose
[One sentence: What does this package do?]

## Contract
- **Input**: [What does it accept?]
- **Output**: [What does it return?]
- **Guarantees**: [What can users rely on?]
- **Limitations**: [What doesn't it do?]

## Usage Example
[Simple code example showing typical usage]

## Architecture
- **Links**:
  - [ADR-006: Package Organization](../../docs/decisions/adr-006-...)
  - [Provider System Overview](../../docs/architecture/provider-system.md)
- **Dependencies**: [What other packages does this use?]
- **Used by**: [What uses this package?]

## Testing
- Unit tests: [What's tested in isolation?]
- Integration tests: [What requires real dependencies?]
- Platform requirements: [Windows only? Docker required?]

## Development
- [Platform-specific setup if needed]
- [How to run tests]
- [How to debug]
```

---

## Migration Phases

### Phase 1: Foundation Utilities ✅ Started

- [x] `pkg/crypto` - Already partially extracted
- [ ] `pkg/powershell` - Extract from internal/provider/hyperv/powershell.go

### Phase 2: Platform Libraries

- [ ] `pkg/hyperv` - Extract from internal/provider/hyperv
  - [ ] Core VM operations
  - [ ] `pkg/hyperv/psdirect` subpackage
- [ ] `pkg/docker` - Extract from internal/provider/docker

### Phase 3: Provider Layer

- [ ] `pkg/provider` - Interface + types (already started in pkg/provider/proto)
- [ ] `pkg/provider/hyperv` - Uses pkg/hyperv + pkg/hyperv/psdirect
- [ ] `pkg/provider/docker` - Uses pkg/docker
- [ ] `pkg/provider/mock` - Move from internal

### Phase 4: Supporting Systems

- [ ] `pkg/hooks` - Move from internal/hooks
- [ ] `pkg/client` - Create SDK for API access (v1 or v2)

### Phase 5: Internal Refactor (v1 features)

- [ ] `internal/core/allocator` - NEW: Resource allocation orchestrator
- [ ] `internal/core/pool` - Refactor to use Allocator
- [ ] `internal/core/sandbox` - Refactor to use Allocator
- [ ] `internal/core/scheduler` - Preheating + recycling

### Phase 6: Clean up

- [ ] Remove old internal/provider/ directory
- [ ] Remove old internal/hooks/ directory
- [ ] Remove old internal/crypto/ directory
- [ ] Update all imports across codebase
- [ ] Ensure all tests pass

---

## Testing Strategy

### Unit Tests (per package)

```bash
go test ./pkg/powershell        # Mock PS responses
go test ./pkg/hyperv            # Mock powershell layer
go test ./pkg/provider/hyperv   # Mock hyperv layer
go test ./internal/core/pool    # Mock provider interface
```

### Integration Tests

```bash
go test ./tests/integration/hyperv   # Real Hyper-V (Windows only)
go test ./tests/integration/docker   # Real Docker
```

### E2E Tests

```bash
go test ./tests/e2e             # Full flows with real providers
```

---

## Context Window Benefits

### Scenario: "Improve PowerShell execution reliability"

```bash
cd pkg/powershell
# Context needed:
# - How to execute PowerShell from Go
# - Error handling, timeouts, marshalling
# - NO need to understand Hyper-V, providers, pools, etc.
```

### Scenario: "Add VM checkpoint support"

```bash
cd pkg/hyperv
# Context needed:
# - Hyper-V operations (VM, VHD, checkpoint)
# - pkg/powershell interface (execute commands)
# - NO need to understand provider contract, pool management, etc.
```

### Scenario: "Improve Hyper-V provider hook execution"

```bash
cd pkg/provider/hyperv
# Context needed:
# - provider.Provider interface (contract)
# - pkg/hyperv API (VM operations)
# - pkg/hyperv/psdirect API (exec in VM)
# - pkg/hooks API (execute hooks)
# - NO need to understand pool replenishment, storage, etc.
```

### Scenario: "Fix pool replenishment race condition"

```bash
cd internal/core/pool
# Context needed:
# - Pool orchestration logic
# - provider.Provider interface (abstract)
# - Storage interface (abstract)
# - NO need to understand Hyper-V/Docker specifics
```

---

## Open Questions

1. **pkg/client timing**: Build in v1 or wait for v2 when we have a stable API?

2. **pkg/hyperv/network**: If networking gets complex, should it be a subpackage or stay in hyperv.go?

3. **v2 distributed agent**: Keep in internal/agent until v2? Or extract pkg/agent early?

4. **Testing approach**: Mock at provider interface level, or deeper (hyperv/docker level)?

5. **Provider plugins**: Future consideration - should providers be Go plugins (.so files) or always compiled in?

---

## Success Criteria

✅ **Package structure complete when:**

- [ ] Every pkg/ package has clear contract + README
- [ ] All pkg/ packages can be tested independently
- [ ] No pkg/ → internal/ dependencies (enforced by Go)
- [ ] Can work on any pkg/ package with < 1000 lines of context
- [ ] All tests pass (unit + integration + e2e)
- [ ] Documentation updated (architecture docs, CLAUDE.md)
