# CLAUDE.md - AI Assistant Guide for Boxy

## General Guidelines

- During planning, try not to bloat the context window with large amounts of code unless specifically requested.
- Markdown files should have a newline after headers
- Go files should have unit tests
- Run `yamllint` against all YAML files before merging; config lives in `.yamllint.yaml`
- Other agent guides (e.g., `CLAUDE.md`, `GEMINI.md`, future equivalents) should defer to and reference this `AGENTS.md` as the single source of truth for assistant instructions
- For Python-based tools, prefer invoking via `uv` (`uvx <tool>`) when available instead of global installs
- Use the `internal/core/allocator` package for pool → sandbox orchestration; avoid calling pool managers directly from sandbox flows

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

**Resource States:**

- **Provisioned** - Created but cold (stopped/not running)
- **Ready** - Running and warm (preheated, available for allocation)
- **Allocated** - In use by a sandbox
- **Destroyed** - Cleaned up and removed

**Key principle:** Resources are NEVER reused. After allocation, resources are destroyed to ensure cleanliness.

### Sandbox

A **time-bound collection of allocated resources**. Think of it as a "disposable environment" - like Windows Sandbox, but cross-platform.

A sandbox might contain:

- 3 server VMs (Windows Server)
- 1 client VM (Windows 10)
- 2 containers (Linux)

Sandboxes are:

- **Time-bound** - Auto-expire after specified duration
- **Isolated** - Each sandbox is completely independent
- **Ephemeral** - Destroyed when no longer needed
- **Clean** - Resources never reused, always fresh

**Primary use case:** Quick testing environments (test installer, validate config, run experiments)

### Pool

A **self-replenishing collection of resources of the same type**. Pools ensure resources are always available:

- Maintains minimum count of resources (mix of cold and warm)
- Automatically provisions replacements when resources allocated
- Supports **preheating** (keeping some resources running for instant allocation)
- Automatic **recycling** (refreshing resources regularly to prevent drift)

**Example Pool Configurations:**

```yaml
pools:
  - name: win-server-2022
    type: vm
    backend: hyperv
    min_ready: 10        # Total resources (cold + warm)
    max_total: 20

    preheating:
      enabled: true
      count: 3           # Keep 3 running/warm for instant allocation
      recycle_interval: 1h  # Refresh resources hourly

  - name: ubuntu-containers
    type: container
    backend: docker
    min_ready: 5
    max_total: 20

    preheating:
      enabled: true
      count: 5           # All preheated (containers start fast)
```

**Cold vs Warm Resources:**

- **Cold** (Provisioned): Created but stopped - takes 30-60s to start and allocate
- **Warm** (Ready): Running and ready - instant allocation (< 5s)
- **Preheating**: Pool keeps configurable count of warm resources

### Allocator (Internal Component)

An **internal orchestration component** that manages resource movement between pools and sandboxes.

**Not user-facing** - users interact with Pools and Sandboxes. Allocator works behind the scenes to:

- Track resource ownership
- Coordinate allocation from pool to sandbox
- Run on_allocate hooks
- Handle resource release/destruction

**Architecture:**

```text
Pool (manages unallocated) ←─── Allocator (orchestrates) ───→ Sandbox (manages allocated)
```

### Backend Providers

Plugins that interface with specific virtualization/containerization platforms:

- **Hyper-V** - Windows VMs (primary for v1-prerelease)
- **VMware** - Cross-platform VMs
- **Docker** - Containers (for testing/development)
- **KVM/QEMU** - Linux VMs
- **Podman** - Container alternative

### Hook System

Lifecycle hooks allow customization at specific points in the resource lifecycle:

**on_provision** - Runs after provider creates resource (cold state)

- **Purpose**: Validation, snapshots, software installation
- **Timing**: During pool replenishment (can be slow, user not waiting)
- **Use for**: Heavy setup tasks, system configuration

**on_allocate** - Runs when user requests resource

- **Purpose**: User-specific personalization
- **Timing**: User is waiting (MUST be fast - seconds, not minutes)
- **Use for**: Creating user accounts, granting access, setting hostname

**Example:**

```yaml
pools:
  - name: win-test-vms

    hooks:
      on_provision:
        - type: script
          shell: powershell
          inline: |
            # Validate VM is accessible
            Test-Connection localhost -Count 1
            # Take snapshot
            Checkpoint-VM -Name $env:COMPUTERNAME -SnapshotName "Clean"

      on_allocate:
        - type: script
          shell: powershell
          inline: |
            # Create user with auto-generated password
            New-LocalUser -Name "${username}" -Password (ConvertTo-SecureString "${password}" -AsPlainText -Force)
            Add-LocalGroupMember -Group "Administrators" -Member "${username}"
            # Grant RDP access
            Set-ItemProperty -Path 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name "fDenyTSConnections" -Value 0
```

**Key distinction:**

- `on_provision` = "prepare base image for pool" (runs once when resource created)
- `on_allocate` = "customize for specific user" (runs each time allocated)

## Architecture Vision

### Component Overview

```text
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

```text
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

```text
User: "I need a Windows Server 2022 VM for 2 hours"
Boxy:
  1. Allocates VM from win-server-2022 pool
  2. Returns RDP connection info + generated credentials
  3. Triggers background replenishment of pool
  4. Sets 2-hour expiration timer
  5. After 2 hours: destroys VM, cleans up data
```

### UC2: Complex Lab Environment

```text
User: "I need an AD lab with 3 DCs, 1 client, 2 web servers"
Boxy:
  1. Creates sandbox with 6 resources
  2. Provisions from multiple pools
  3. Configures networking (if supported)
  4. Returns connection details for each resource
  5. User can extend time or manually destroy
```

### UC3: Auto-scaling CI/CD Runners

```text
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
- **Create architecture diagrams** - Visual diagrams are extremely valuable for planning and design
  - Show component relationships
  - Illustrate data flows
  - Map entire system architecture
  - Use ASCII art for easy inclusion in docs

**Step 2: Planning**

- Break down into tasks
- Identify dependencies
- Consider testing approach
- Document decisions
- **NO TIME ESTIMATES** - Focus on what needs to be done, not when
  - Don't suggest "this will take 2 weeks"
  - Don't estimate effort in planning docs
  - User decides timeline, you focus on completeness

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

**Step 5: Definition of Done** ⚠️ **MANDATORY CHECKLIST**

Before claiming any component is "done" or "complete", you **MUST** verify:

✅ **Code Quality:**

- [ ] Run `golangci-lint run ./path/to/package/...` - All linters pass with zero errors
- [ ] Code builds successfully (`go build .`)
- [ ] No compiler warnings or errors

✅ **Testing:**

- [ ] Unit tests written for all functions (`*_test.go` files)
- [ ] All tests pass (`go test -v .`)
- [ ] Test coverage is reasonable (aim for >80% on critical paths)
- [ ] Edge cases and error scenarios tested

✅ **Documentation:**

- [ ] Package has README.md with:
  - Purpose/contract clearly stated
  - Usage examples that actually work
  - Dependencies listed
  - Architecture links
- [ ] All exported functions have godoc comments
- [ ] Complex logic has inline comments explaining why

✅ **Integration:**

- [ ] Package integrates with dependent packages
- [ ] Import paths are correct
- [ ] No circular dependencies

**Never say something is "done" or "complete" without completing this checklist.**

If you skip any step, explicitly state which steps remain and why.

**Step 6: Use Planning Docs for Roadmapping**

- **v1/v2/v3 Planning Docs** - Use versioned planning documents to manage work between sessions
  - `V1_IMPLEMENTATION_PLAN.md` - Complete v1 specification
  - `V2_IMPLEMENTATION_PLAN.md` - Future v2 features
  - `V3_IMPLEMENTATION_PLAN.md` - Long-term vision
- Benefits of this approach:
  - Clear scope per version
  - Easy to reference what's in/out of each release
  - Maintains context between sessions
  - Serves as project roadmap
  - Can move features between versions as priorities change
- Example structure:

  ```text
  docs/
    V1_IMPLEMENTATION_PLAN.md    # Current release (distributed agents, multi-tenancy)
    V2_IMPLEMENTATION_PLAN.md    # Next release (VSCode plugin, advanced scheduling)
    V3_IMPLEMENTATION_PLAN.md    # Future (multi-cloud, GPU workloads)
  ```

### 3. Commit & Push Practices

**Use Conventional Commits:**

```text
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

```text
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

**AGENTS.md is for:**

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

### Go docs & Package documentation

- Always prefer `go doc` (or `godoc` for a local web server) to inspect package-level documentation and public APIs before making changes.
  - Quick examples:
    - `go doc ./pkg/provider` — view package documentation for `pkg/provider`.
    - `go doc ./pkg/provider Provider` — view the docs for the exported symbol `Provider`.
    - `godoc -http=:6060` and open `http://localhost:6060/pkg/` to browse documentation for all packages locally.
- When updating or adding exported APIs, update the corresponding godoc comments as part of your change. This includes:
  - Package-level comments at the top of the Go source file (the `package` comment).
  - Comments for exported types, functions, methods, and constants.
  - Code examples and usage snippets — keep them accurate.
- If a package has a `README.md` in its directory, make this a doc.go file instead, and ensure it is kept up to date with any API changes.
- Checklist for package-level changes (include in PR descriptions where applicable):
  - [ ] Run `go doc <package>` to review current docs before the change.
  - [ ] Update godoc comments for any modified or new exported symbols.
  - [ ] Update package `README.md` or examples if present.
  - [ ] Run `go test ./...` and `golangci-lint run` and fix any linter issues related to docs/comments.
  - [ ] Link to the updated `docs/` pages or ADRs if broader API contracts changed.

> 💡 Tip: The `go vet` command and many linters in the `golangci-lint` suite will detect missing or malformed comments for exported identifiers — include these tools in your pre-PR checks.

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

### Coding Guidelines

- Follow Go best practices (effective Go, idiomatic patterns)
- Use context for cancellations/timeouts
- Handle errors explicitly
- Use interfaces for abstractions
- Keep functions small and focused
- Favor composition over inheritance
- Separate concerns into packages
- Modularize code for testability
- Write clear godoc comments
- Use logging for observability
- Follow SOLID principles if applicable
- Write clean, maintainable code
- Keep code flat if possible - avoid deep nesting

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

```text
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

**Phase**: v1 Implementation Planning
**Completed**:

- ✅ v1-prerelease Phase 1: Core functionality (pools, sandboxes, Docker provider)
- ✅ Single-host embedded architecture working
- ✅ Critical security fix (crypto/rand for password generation)
- ✅ Architectural review completed
- ✅ v1 implementation plan created
- ✅ Use cases documented
- ✅ Documentation updated for consistency

**Current Focus**: v1 Architecture Refactor
**Next Steps** (see [V1_IMPLEMENTATION_PLAN.md](docs/V1_IMPLEMENTATION_PLAN.md)):

1. ⏳ Implement Allocator component (Pool/Sandbox peer architecture)
2. ⏳ Implement preheating & recycling system
3. ⏳ Update terminology (on_provision, on_allocate hooks)
4. ⏳ Implement multi-tenancy (users, teams, API tokens)
5. ⏳ Add Pool as first-class component (CLI commands)
6. ⏳ Base image validation system
7. ⏳ Comprehensive testing (unit, integration, E2E)
8. ⏳ Documentation finalization

**Future** (v2+):

- Distributed agent architecture (ADR-004) - deferred to v2
- Network isolation (overlay networks)
- Advanced retry strategies
- Pool layering (v3)

**Key Documents**:

- [V1 Implementation Plan](docs/V1_IMPLEMENTATION_PLAN.md) - Comprehensive v1 specification
- [Use Cases](docs/USE_CASES.md) - Primary and secondary use cases
- [ADR-005: Pool/Sandbox Peer Architecture](docs/decisions/adr-005-pool-sandbox-peer-architecture.md) - New architecture
- [ADR-004: Distributed Agent Architecture](docs/decisions/adr-004-distributed-agent-architecture.md) - v2 feature

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

```text
POST   /api/v1/sandboxes          # Create sandbox
GET    /api/v1/sandboxes/:id      # Get sandbox details
DELETE /api/v1/sandboxes/:id      # Destroy sandbox
PATCH  /api/v1/sandboxes/:id      # Extend time
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

---

## Local Development and Tooling

To ensure code quality, consistency, and security, this project utilizes several local development tools. New contributors should install and use them as described below.

### Go Tools

These tools are standard in the Go ecosystem and are installed via `go install`.

- **`goimports`**: Automatically formats code and manages imports.
  - **Installation**: `go install golang.org/x/tools/cmd/goimports@latest`
  - **Usage**: Run `goimports -w .` in the project root before committing to format your changes. Most Go editors can be configured to run this on save.

- **`govulncheck`**: Scans your project's dependencies for known vulnerabilities.
  - **Installation**: `go install golang.org/x/vuln/cmd/govulncheck@latest`
  - **Usage**: Run `govulncheck ./...` in the project root to check for vulnerabilities. This is especially important before proposing a change that adds or updates dependencies.

- **`go doc` / `godoc`**: Use to inspect package-level documentation and public APIs.
  - **Installation**: `go doc` is included with the Go toolchain; `godoc` can be installed with `go install golang.org/x/tools/cmd/godoc@latest`.
  - **Usage**: Run `go doc <package>` locally to quickly view package docs, or `godoc -http=:6060` and open `http://localhost:6060/pkg/` for a browsable package site. Use these tools to verify docs before submitting PRs.

### Linters and Scanners

These tools are installed in the `.bin/` directory within the project.

- **`hadolint`**: A linter for Dockerfiles to enforce best practices.
  - **Installation**: Managed via a script. See initial setup.
  - **Usage**: Run `./.bin/hadolint Dockerfile` to check the project's Dockerfile. The configuration is in `.hadolint.yaml`.

- **`gitleaks`**: A scanner that checks for secrets (like API keys and passwords) in your code and commit history.
  - **Installation**: Managed via a script. See initial setup.
  - **Usage**: Run `./.bin/gitleaks detect --source . -v` to scan the repository. This should be run before committing to prevent accidentally exposing secrets. Configuration is in `.gitleaks.toml`.
