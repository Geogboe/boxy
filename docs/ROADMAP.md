# Boxy Development Roadmap

## Vision

Create a sandboxing orchestration tool that makes it trivial to spin up mixed environments (VMs, containers, processes) with automatic lifecycle management and pool-based resource provisioning.

## Phased Approach

### Phase 0: Foundation & Planning (Current)

**Goal**: Make architectural decisions and set up project foundation

- [x] Define project vision and concepts
- [x] Create CLAUDE.md for development guidelines
- [ ] Research and select technology stack
- [ ] Design core architecture
- [ ] Document key architectural decisions (ADRs)
- [ ] Set up project structure

**Success Criteria**: Clear architecture, technology choices made, project skeleton ready

---

### Phase 1: MVP - Single Backend (Docker)

**Goal**: Prove the core concept with a single, easy-to-test backend

**Scope**:

- ✅ Docker-only backend (no VMs yet)
- ✅ Simple pool management (min_ready, auto-replenish)
- ✅ Basic CLI for pool/sandbox operations
- ✅ In-memory or SQLite state storage
- ✅ Container lifecycle management (create, destroy, cleanup)
- ✅ Basic credential generation
- ❌ No Web UI (CLI only)
- ❌ No API server (direct library usage)
- ❌ No multi-tenancy
- ❌ No authentication

**Features**:

1. **Pool Management**
   - Define container pools via config
   - Maintain min_ready count
   - Auto-replenish on allocation

2. **Sandbox Operations**
   - Create sandbox with N containers
   - Get connection info (ports, credentials)
   - Set expiration time
   - Manual and auto-cleanup

3. **CLI**

   ```bash
   boxy pool create --name ubuntu --image ubuntu:22.04 --min-ready 3
   boxy pool list
   boxy sandbox create --containers 2 --pool ubuntu --duration 2h
   boxy sandbox list
   boxy sandbox destroy <id>
   ```

**Deliverables**:

- Working Docker provider
- Pool manager with auto-replenishment
- Sandbox orchestrator
- Basic CLI
- Unit + integration tests
- Documentation

**Success Criteria**:

- Can create a pool of 3 Ubuntu containers
- Can request a sandbox with 2 containers
- Containers auto-expire after set duration
- Pool auto-replenishes when containers allocated
- All core functionality has tests

**Estimated Effort**: 2-3 weeks

---

### Phase 2: Plugin System & Multi-Backend

**Goal**: Generalize to support multiple backend providers

**Scope**:

- Plugin/provider abstraction layer
- Add second backend (KVM or Hyper-V)
- Provider-specific configuration
- Connection info abstraction (RDP, SSH, ports)

**Features**:

1. **Provider Interface**
   - Standardized provider contract
   - Provider registration system
   - Provider-specific config validation

2. **Multiple Providers**
   - Docker provider (refactored)
   - KVM/libvirt provider OR Hyper-V provider
   - Provider selection in pool config

3. **Connection Abstractions**
   - Different connection types (SSH, RDP, exec, ports)
   - Provider-specific credential formats
   - Unified connection info structure

**Deliverables**:

- Provider interface definition
- Refactored Docker provider
- Second provider implementation
- Provider selection mechanism
- Updated CLI to handle multiple providers
- Provider-specific tests

**Success Criteria**:

- Can create pools with different providers
- Can mix provider types in same installation
- Connection info adapts to provider type
- Each provider independently tested

**Estimated Effort**: 2-3 weeks

---

### Phase 3: REST API & Service Mode

**Goal**: Run Boxy as a service with HTTP API

**Scope**:

- REST API server
- Service/daemon mode
- Persistent state storage (PostgreSQL)
- Basic API authentication
- Background lifecycle management

**Features**:

1. **API Server**

   ```text
   POST   /api/v1/pools
   GET    /api/v1/pools
   GET    /api/v1/pools/:id
   POST   /api/v1/sandboxes
   GET    /api/v1/sandboxes/:id
   DELETE /api/v1/sandboxes/:id
   PATCH  /api/v1/sandboxes/:id/extend
   ```

2. **Service Daemon**
   - Run as background service
   - Background pool replenishment
   - Background sandbox cleanup (expiration)
   - Graceful shutdown handling

3. **Persistent Storage**
   - PostgreSQL for state
   - Redis for job queue (optional)
   - Migration system

4. **Authentication**
   - API key authentication
   - Basic RBAC (admin vs user)

**Deliverables**:

- REST API implementation
- Service daemon mode
- Database migrations
- API authentication
- Background workers
- API tests
- API documentation

**Success Criteria**:

- API can manage pools and sandboxes
- Service runs continuously
- State persists across restarts
- Background cleanup works reliably
- API is authenticated and documented

**Estimated Effort**: 3-4 weeks

---

### Phase 4: Web UI

**Goal**: Visual interface for managing Boxy

**Scope**:

- Web dashboard
- Pool monitoring
- Sandbox management
- Real-time status updates

**Features**:

1. **Dashboard**
   - Pool status overview
   - Active sandboxes count
   - Resource utilization

2. **Pool Management**
   - View pools and health
   - Create/modify pools
   - See available capacity

3. **Sandbox Management**
   - List active sandboxes
   - Create new sandboxes
   - View connection info
   - Extend/destroy sandboxes

4. **Real-time Updates**
   - WebSocket or SSE for live updates
   - Pool replenishment status
   - Sandbox expiration timers

**Deliverables**:

- Web UI (React/Vue/Svelte)
- Backend API enhancements
- Real-time update mechanism
- User documentation

**Success Criteria**:

- Can perform all operations via UI
- Real-time status updates work
- UI is responsive and intuitive

**Estimated Effort**: 3-4 weeks

---

### Phase 5: Advanced Features

**Goal**: Production-ready features

**Scope**:

- Multi-tenancy
- Advanced pool strategies (warm/cold/hybrid)
- Resource templates/images
- Networking between sandbox resources
- Cost tracking
- Quotas and limits
- Observability (metrics, tracing)

**Features**:

1. **Multi-tenancy**
   - Tenant isolation
   - Per-tenant quotas
   - Usage tracking

2. **Advanced Pools**
   - Warm pools (pre-provisioned)
   - Cold pools (on-demand)
   - Hybrid pools (adaptive)
   - Autoscaling based on demand

3. **Templates**
   - Resource templates (pre-configured images)
   - Template marketplace
   - Custom image management

4. **Networking**
   - Network isolation
   - Inter-resource networking
   - VPN/overlay networks

5. **Observability**
   - Prometheus metrics
   - Distributed tracing
   - Structured logging
   - Alerting

**Deliverables**:

- Multi-tenant support
- Advanced pool strategies
- Template system
- Network management
- Metrics and observability
- Production deployment guides

**Success Criteria**:

- Production-ready
- Scalable to multiple tenants
- Observable and debuggable
- Cost-effective resource usage

**Estimated Effort**: 6-8 weeks

---

## Current Focus: Phase 0 → Phase 1

### Immediate Next Steps

1. ✅ Create roadmap (this document)
2. ⏳ Research technology stack
3. ⏳ Make key architectural decisions
4. ⏳ Set up project structure
5. ⏳ Implement MVP (Phase 1)

### Open Questions for Phase 1

See architectural research document for details.

---

## Success Metrics

### MVP Success (Phase 1)

- [ ] 90%+ test coverage for core logic
- [ ] Can provision 10 containers in <30 seconds
- [ ] Pool replenishment works reliably
- [ ] Zero leaked containers after sandbox cleanup
- [ ] Documentation complete enough for contributors

### Production Ready (Phase 5)

- [ ] 99.9% uptime SLA
- [ ] Supports 100+ concurrent sandboxes
- [ ] Multi-tenant capable
- [ ] Full observability
- [ ] Security audit passed

---

## Risk Assessment

### High Risk

- **Complexity Creep**: Trying to do too much too soon
  - *Mitigation*: Strict phase discipline, MVP-first approach

- **Resource Leaks**: Containers/VMs not cleaned up
  - *Mitigation*: Comprehensive testing, background cleanup jobs, alerts

- **Security Issues**: Credential leaks, insufficient isolation
  - *Mitigation*: Security review at each phase, audit logging

### Medium Risk

- **Backend Provider Issues**: Provider-specific bugs and quirks
  - *Mitigation*: Extensive provider testing, clear error handling

- **State Consistency**: Race conditions in pool management
  - *Mitigation*: Proper locking, transactions, testing

### Low Risk

- **Performance**: Slow provisioning
  - *Mitigation*: Profiling, optimization, caching

---

## Decision Log

Major decisions will be documented as ADRs in `/docs/decisions/`.

### Decisions Needed (Phase 0)

- [ ] ADR-001: Technology stack (language, frameworks)
- [ ] ADR-002: State storage approach
- [ ] ADR-003: Plugin/provider architecture
- [ ] ADR-004: Configuration format (YAML, TOML, etc.)

### Decisions Made

- None yet (project just started)

## Detailed Phase Plans and Implementation Guides

For more granular details on specific phases or implementation aspects, refer to these documents:

-   **v1 Prerelease Implementation**: [docs/v1-prerelease/README.md](v1-prerelease/README.md) - Provides a detailed breakdown of features planned for the v1 prerelease.
-   **v2 Prerelease Implementation**: [docs/v2-prerelease/README.md](v2-prerelease/README.md) - Details features planned for the v2 prerelease.
-   **Distributed Agent Architecture Implementation**: [docs/IMPLEMENTATION_ROADMAP.md](IMPLEMENTATION_ROADMAP.md) - Offers a tactical, step-by-step implementation plan for the distributed agent architecture.