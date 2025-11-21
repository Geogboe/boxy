# CLAUDE.md - AI Assistant Guide for Boxy

## Project Overview

**Boxy** is a sandboxing orchestration tool that simplifies the management of mixed virtual environments. It solves the complex problem of spinning up and managing heterogeneous resources (VMs, containers, processes) across different platforms (Windows, Linux) with automated lifecycle management.

### The Problem Boxy Solves

Currently, creating mixed environments with VMs, containers, and processes across Windows and Linux is cumbersome and requires manual orchestration. Boxy provides a unified interface to:

- Define resource pools that auto-replenish
- Provision heterogeneous environments on-demand
- Manage lifecycle automatically (creation, allocation, expiration, cleanup)
- Abstract away backend complexity (Hyper-V, VMware, Docker, etc.)

### Core Value Proposition

**"Request a sandbox, get working resources instantly, return them when done."**

## Core Concepts & Terminology

### Resources
Individual compute units that can be provisioned:
- **VMs** (Virtual Machines) - Full OS instances
- **Containers** - Lightweight isolated environments
- **Processes** - Managed application instances

### Sandbox
A **logical collection of resources** allocated to fulfill a specific request. A sandbox might contain:
- 3 server VMs (Windows Server)
- 1 client VM (Windows 10)
- 2 containers (Linux)

Sandboxes are:
- Time-bound (auto-expire after duration)
- Isolated (each sandbox is independent)
- Ephemeral (destroyed when no longer needed)

### Pool
A **self-replenishing collection of pre-provisioned resources of the same type**. Pools ensure resources are always available:

- When a resource is allocated from the pool → automatically provision a replacement
- Maintain a minimum count of ready resources
- Support different provisioning strategies (warm, cold, hybrid)

**Example Pool Configurations:**
```yaml
pools:
  - name: win-server-2022
    type: vm
    backend: hyperv
    min_ready: 3
    max_total: 10

  - name: ubuntu-containers
    type: container
    backend: docker
    min_ready: 5
    max_total: 20
```

### Backend Providers
Plugins that interface with specific virtualization/containerization platforms:
- **Hyper-V** - Windows VMs
- **VMware** - Cross-platform VMs
- **Docker** - Containers
- **KVM/QEMU** - Linux VMs
- **Podman** - Container alternative

## Architecture Vision

### Component Overview

```
┌─────────────────────────────────────────────────────────┐
│                    User Interfaces                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────────┐ │
│  │   CLI    │  │  Web UI  │  │   API    │  │  SDK    │ │
│  └──────────┘  └──────────┘  └──────────┘  └─────────┘ │
└─────────────────────────────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────┐
│                    Boxy Service Core                     │
│  ┌────────────────────────────────────────────────────┐ │
│  │  Request Handler │ Scheduler │ Lifecycle Manager  │ │
│  └────────────────────────────────────────────────────┘ │
│  ┌────────────────────────────────────────────────────┐ │
│  │  Pool Manager │ Resource Allocator │ Auth/Access  │ │
│  └────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────┐
│                   Plugin System                          │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────────┐ │
│  │ Hyper-V  │  │  VMware  │  │  Docker  │  │   KVM   │ │
│  │  Plugin  │  │  Plugin  │  │  Plugin  │  │ Plugin  │ │
│  └──────────┘  └──────────┘  └──────────┘  └─────────┘ │
└─────────────────────────────────────────────────────────┘
```

### Key Architectural Decisions Needed

#### 1. **State Management**
- Where is pool/sandbox state stored? (Database, distributed store, file-based?)
- How do we handle state consistency across restarts?
- What happens to in-flight provisioning during service restart?

#### 2. **Pool Provisioning Strategy**
Consider these tradeoffs:

**Warm Pools** (resources running and ready)
- ✅ Instant allocation
- ❌ High cost (resources always running)
- 💡 Best for: High-demand, performance-critical scenarios

**Cold Pools** (resources defined but not running)
- ✅ Low cost
- ❌ Provisioning delay
- 💡 Best for: Cost-sensitive, delay-tolerant scenarios

**Hybrid Pools** (min warm, overflow cold)
- ✅ Balance of cost and speed
- ✅ Adaptive to demand
- 💡 Best for: Variable workloads

**Recommendation**: Start with configurable strategies per pool.

#### 3. **Plugin Contract**
Each backend plugin must implement:
```
interface BackendProvider {
  // Lifecycle
  provision(spec: ResourceSpec): Promise<Resource>
  destroy(resourceId: string): Promise<void>

  // State management
  getStatus(resourceId: string): Promise<ResourceStatus>

  // Connection details
  getConnectionInfo(resourceId: string): Promise<ConnectionInfo>

  // Health
  healthCheck(): Promise<boolean>
}
```

#### 4. **Credential Management**
- Auto-generate random credentials per resource
- Secure storage (encrypted at rest)
- Rotation policies
- Cleanup after resource destruction

## Use Cases & User Stories

### UC1: Single VM for Testing
```
User: "I need a Windows Server 2022 VM for 2 hours"
Boxy:
  1. Allocates VM from win-server-2022 pool
  2. Returns RDP connection info + generated credentials
  3. Triggers background replenishment of pool
  4. Sets 2-hour expiration timer
  5. After 2 hours: destroys VM, cleans up data
```

### UC2: Complex Lab Environment
```
User: "I need an AD lab with 3 DCs, 1 client, 2 web servers"
Boxy:
  1. Creates sandbox with 6 resources
  2. Provisions from multiple pools
  3. Configures networking (if supported)
  4. Returns connection details for each resource
  5. User can extend time or manually destroy
```

### UC3: Auto-scaling CI/CD Runners
```
CI System: Webhook triggers on commit
Boxy:
  1. Provisions fresh container from pool
  2. Runs tests in isolated environment
  3. Returns results
  4. Destroys container
  5. Pool auto-replenishes
```

## Development Guidelines for AI Assistants

### 1. Critical Thinking & Architectural Review

**Always approach tasks as a skeptical architect first:**

✅ **DO:**
- Question design decisions that seem inefficient or overly complex
- Propose simpler alternatives when appropriate
- Identify edge cases and failure scenarios
- Consider security, scalability, and cost implications
- Ask for clarification when requirements are ambiguous

❌ **DON'T:**
- Blindly implement features without understanding the bigger picture
- Accept "that's how it's always been done" as justification
- Ignore technical debt or architectural concerns
- Implement without considering testing implications

**Example Critical Questions:**
- "Should pools support autoscaling based on demand patterns?"
- "How do we handle network isolation between sandboxes?"
- "What's the cleanup strategy if provisioning fails mid-sandbox?"
- "Do we need multi-tenancy support from day one?"

### 2. Workflow: Architect → Plan → Build → Test

**Step 1: Architectural Review**
- Understand the requirement
- Challenge assumptions
- Propose alternatives
- Get alignment

**Step 2: Planning**
- Break down into tasks
- Identify dependencies
- Consider testing approach
- Document decisions

**Step 3: Build & Run**
- Implement incrementally
- Run/test as you build
- Use Docker for dependencies
- Stub/mock unavailable components

**Step 4: Test Thoroughly** ⚠️ **CRITICAL**
- Unit tests (individual functions)
- Integration tests (component interactions)
- **End-to-end tests (full user flows) - REQUIRED before marking as complete**
- Error scenarios (what happens when things fail?)
- **MUST run `go test ./tests/e2e/` and verify all tests pass before saying "done"**
- **MUST actually test CLI commands work (not just compile)**

### 3. Commit & Push Practices

**Use Conventional Commits:**
```
feat: add Hyper-V backend plugin
fix: resolve pool replenishment race condition
docs: update API documentation for sandbox creation
test: add integration tests for Docker provider
refactor: simplify resource allocation logic
chore: update dependencies
```

**Commit Often:**
- Small, focused commits
- Each commit should be a logical unit
- Push frequently (don't wait for perfection)
- No need to ask for permission to commit/push

### 4. DRY & Don't Reinvent the Wheel

**Use existing, reputable packages when available:**

✅ **DO use established libraries for:**
- HTTP servers (Express, FastAPI, Gin, etc.)
- Database ORMs (TypeORM, Prisma, GORM, SQLAlchemy)
- Authentication (Passport, OAuth libraries)
- Logging (Winston, Zap, logrus)
- Validation (Joi, Zod, validator)
- CLI frameworks (Commander, Cobra, Click)
- Testing (Jest, pytest, go test)

❌ **DON'T reinvent:**
- HTTP request handling
- JSON parsing
- Cryptography primitives
- Standard data structures
- Common algorithms

✅ **DO write custom code for:**
- Boxy-specific domain logic
- Plugin abstractions
- Pool management algorithms
- Resource orchestration
- Novel workflows specific to this project

### 5. Documentation Structure

**Keep documentation organized:**

```
/docs
  /architecture
    - overview.md
    - plugin-system.md
    - pool-management.md
  /api
    - rest-api.md
    - sdk-reference.md
  /guides
    - getting-started.md
    - creating-plugins.md
    - deployment.md
  /decisions
    - adr-001-tech-stack.md
    - adr-002-state-storage.md
```

**CLAUDE.md is for:**
- AI assistant guidance
- Development workflows
- High-level architecture
- Conventions and standards

**docs/ is for:**
- User-facing documentation
- API references
- Detailed architecture
- Tutorials and guides

### 6. Code Comments and TODOs

**Use TODOs for future work**:
```go
// TODO(mvp2): Add support for multi-resource sandboxes
// TODO(phase3): Implement overlay networking with WireGuard
// TODO: Implement retry logic with exponential backoff
// TODO(security): Encrypt this field before storing
```

**Guidelines**:
- Use TODOs for actionable future items
- Include phase/milestone if relevant (mvp2, phase3, etc)
- Don't overuse - keep focused on important items
- Remove TODOs when completed

### 7. Testing Philosophy

**Test at multiple levels:**

**Unit Tests:**
```go
// Test individual components in isolation
func TestHookExecutor_Timeout(t *testing.T) {
  executor := hooks.NewExecutor()
  hook := &Hook{Timeout: 1 * time.Second}

  // Should timeout
  err := executor.Execute(ctx, hook, slowProvider)
  assert.Error(t, err)
  assert.Contains(t, err.Error(), "timeout")
}
```

**Integration Tests:**
```go
// Test components working together
func TestPoolWithDockerProvider(t *testing.T) {
  if testing.Short() {
    t.Skip("skipping integration test")
  }

  provider := docker.NewProvider(logger, encryptor)
  pool := pool.NewManager(config, provider, repo, logger)

  // Test full provision flow with hooks
  resource := pool.Allocate(ctx, "sb-123")
  assert.NotNil(t, resource.ConnectionInfo)
}
```

**End-to-End Tests:**
```go
// Test full user flows
func TestE2E_SandboxWithHooks(t *testing.T) {
  if testing.Short() {
    t.Skip("skipping e2e test")
  }

  // Start Boxy service
  // Create sandbox with hooks
  // Verify hooks executed
  // Verify resource ready
  // Cleanup
}
```

**Use Docker for testing:**
- Docker provider: Real implementation, can actually test
- Hyper-V provider: Stub/mock for testing without Windows
- Tests run on CI (Linux) using Docker

**Stub unavailable components:**
```go
// Stub Hyper-V provider for testing on Linux
type StubHyperVProvider struct {
    vms map[string]*stubVM
}

func (s *StubHyperVProvider) Provision(ctx, spec) (*Resource, error) {
    // Simulate realistic behavior
    time.Sleep(10 * time.Second) // Simulate provision time
    return &Resource{ID: uuid.New().String()}, nil
}
```

### 7. Technology Stack Recommendations

**Language Options:**
- **Go**: Excellent for system tools, concurrency, plugins, CLI
- **Rust**: Maximum performance, safety, but steeper learning curve
- **Python**: Fast prototyping, but consider performance for production
- **TypeScript/Node**: Good for web UI/API, large ecosystem

**Recommendation**: Consider **Go** for core service + CLI, **TypeScript** for web UI

**Why Go?**
- Native plugin support (`plugin` package)
- Excellent concurrency (goroutines for pool management)
- Single binary distribution
- Great CLI libraries (Cobra)
- Strong API frameworks (Gin, Echo)
- Good virtualization libraries (libvirt-go, Docker SDK)

**Database Options:**
- **SQLite**: Simple, embedded, good for single-node
- **PostgreSQL**: Robust, JSONB support, good for production
- **Redis**: Fast state storage, pub/sub for events

**Recommendation**: Start with **PostgreSQL** for state + **Redis** for events/caching

### 8. Security Considerations

⚠️ **Critical Security Requirements:**

1. **Credential Management**
   - Never log credentials
   - Use strong random generation (crypto/rand)
   - Encrypt at rest
   - Auto-rotate where possible

2. **Resource Isolation**
   - Network isolation between sandboxes
   - Prevent resource exhaustion attacks
   - Limit resource access by user/tenant

3. **API Security**
   - Authentication (API keys, OAuth)
   - Authorization (RBAC)
   - Rate limiting
   - Input validation

4. **Audit Logging**
   - Who requested what resource
   - When resources were created/destroyed
   - Any access to credentials

## Code Organization (Proposed)

```
boxy/
├── cmd/
│   ├── boxy/              # CLI tool
│   ├── boxyd/             # Service daemon
│   └── boxy-ui/           # Web UI server
├── internal/
│   ├── core/              # Core domain logic
│   │   ├── pool/          # Pool management
│   │   ├── sandbox/       # Sandbox orchestration
│   │   ├── resource/      # Resource abstractions
│   │   └── scheduler/     # Lifecycle scheduler
│   ├── storage/           # State persistence
│   ├── api/               # REST API handlers
│   └── config/            # Configuration management
├── pkg/
│   ├── provider/          # Plugin interfaces
│   │   ├── hyperv/
│   │   ├── vmware/
│   │   ├── docker/
│   │   └── kvm/
│   └── client/            # SDK for programmatic access
├── web/                   # Web UI (React/Vue/Svelte)
├── docs/                  # Documentation
├── tests/                 # Integration & E2E tests
├── scripts/               # Build & deployment scripts
└── examples/              # Example configurations
```

## Open Questions & Decisions Needed

### 1. Resource Networking
- Should Boxy configure networks between resources in a sandbox?
- Or is that the user's responsibility?
- Do sandboxes need VPN/overlay networks?

### 2. Resource Templates
- Should we support resource templates (pre-configured VMs)?
- Image management system needed?
- Integration with Packer/Vagrant?

### 3. Multi-tenancy
- Day 1 feature or future enhancement?
- Tenant isolation at what level?
- Quota management per tenant?

### 4. Observability
- Metrics collection (Prometheus?)
- Logging aggregation (ELK stack?)
- Distributed tracing?

### 5. Cost Tracking
- Track resource costs per sandbox/user?
- Budget limits and alerts?
- Integration with cloud billing APIs?

## Current Project Status

**Phase**: MVP Complete + Distributed Architecture Planning
**Completed**:
- ✅ MVP Phase 1: Core functionality (pools, sandboxes, Docker provider)
- ✅ Single-host embedded architecture working
- ✅ Distributed agent architecture designed (ADR-004)
- ✅ Protocol Buffers schema defined
- ✅ Security model planned (mTLS, certificate management)
- ✅ Implementation guide created

**Current Focus**: Distributed Agent Implementation
**Next Steps**:
1. ⏳ Implement gRPC services (ProviderService, AgentService)
2. ⏳ Create RemoteProvider implementation
3. ⏳ Create Agent Server implementation
4. ⏳ Implement certificate management commands
5. ⏳ Add `boxy agent serve` command
6. ⏳ Integration and E2E testing
7. ⏳ Security audit and production hardening

**See**:
- [ADR-004: Distributed Agent Architecture](docs/decisions/adr-004-distributed-agent-architecture.md)
- [Implementation Guide](docs/architecture/distributed-agent-implementation.md)
- [Security Guide](docs/architecture/security-guide.md)

## Quick Reference

### Common Commands (Future)
```bash
# Start Boxy service
boxy serve

# Request a VM
boxy request vm --type windows-server --duration 2h

# Create a sandbox
boxy create sandbox --from lab-template.yml

# List active resources
boxy list sandboxes

# Destroy a sandbox
boxy destroy sb-12345

# Manage pools
boxy pool status
boxy pool scale ubuntu-containers --min 5
```

### API Endpoints (Planned)
```
POST   /api/v1/sandboxes          # Create sandbox
GET    /api/v1/sandboxes/:id      # Get sandbox details
DELETE /api/v1/sandboxes/:id      # Destroy sandbox
PATCH  /api/v1/sandboxes/:id      # Extend time

GET    /api/v1/pools              # List pools
GET    /api/v1/pools/:id          # Get pool status
POST   /api/v1/pools              # Create pool
```

---

## Final Notes for AI Assistants

This is an ambitious project with real complexity. Your role is to:

1. **Challenge and improve** the design through thoughtful critique
2. **Build incrementally** with frequent testing
3. **Prioritize simplicity** over premature optimization
4. **Document decisions** so future maintainers understand why
5. **Test thoroughly** because managing VMs/containers has real consequences

When in doubt, ask questions. The goal is to build something robust and maintainable, not just to ship features quickly.

**Remember**: Every VM costs money, every leak of credentials is a security incident, and every bug in lifecycle management leaves resources orphaned. Build with care.
