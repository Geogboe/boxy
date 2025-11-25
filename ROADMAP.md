# Boxy Roadmap

This document outlines the development roadmap for Boxy, organized by priority and phase.

## Current Status (v0.1.0-alpha)

**Working:**
- ✅ Core pool management (provision, allocate, release, destroy)
- ✅ Scratch/shell provider (filesystem-based workspaces)
- ✅ SQLite storage backend
- ✅ Sandbox lifecycle management
- ✅ Hook system (on_provision, on_allocate)
- ✅ HTTP API server
- ✅ CLI commands (serve, pool, sandbox)
- ✅ Connect script with activation/deactivation

**Known Issues:**
- ⚠️ Test suite broken (allocator signature change)
- ⚠️ CLI UX needs polish (no clear usage instructions)
- ⚠️ No E2E tests validating full workflows

---

## Phase 1: Polish & Stability (MVP Hardening)

**Goal:** Make current functionality rock-solid and user-friendly.

### 1.1 Fix Immediate Issues (Week 1)

- [ ] **Fix failing tests** after allocator interface change
  - Update `allocator_test.go` stub to accept `expiresAt` parameter
  - Run full test suite: `go test ./...`
  - Fix any other breakages

- [ ] **Improve CLI UX**
  - Show "how to use" instructions after sandbox creation
  - Display `source <connect-script>` prominently
  - Add examples to help text
  - Consider: Should we add `boxy sandbox connect <id>` that prints the source command?

- [ ] **Add working TODO.md**
  - Session-persistent task tracking
  - Current blockers and decisions needed

### 1.2 End-to-End Testing (Week 1-2)

**ADR Needed:** ADR-010 - E2E Testing Strategy
- Test scope (CLI only? API? Both?)
- Test isolation (shared DB? per-test DB?)
- CI/CD integration approach

**Tasks:**
- [ ] Write E2E test framework using scratch provider
- [ ] Test: Full sandbox lifecycle (create → allocate → use → destroy)
- [ ] Test: Pool replenishment behavior
- [ ] Test: Concurrent sandbox creation
- [ ] Test: Resource cleanup on expiration
- [ ] Test: Error handling and recovery
- [ ] Test: Hook execution (on_provision, on_allocate)

### 1.3 Documentation (Week 2)

- [ ] User guide (getting started, common workflows)
- [ ] Architecture documentation (component diagram, data flow)
- [ ] Provider development guide
- [ ] Troubleshooting guide

**Deliverable:** v0.1.0 - Stable MVP with great UX

---

## Phase 2: Essential Features (Production-Ready)

**Goal:** Add missing functionality for real-world usage.

### 2.1 Runtime Pool Management (Week 3)

**ADR Needed:** ADR-011 - Runtime Pool Scaling
- Should scale commands modify config file or just runtime state?
- How to handle scale-down (which resources to remove)?
- Should we support autoscaling based on demand?

**Commands to Add:**
- [ ] `boxy pool scale <pool-name> --min-ready=N --max-total=M`
- [ ] `boxy pool info <pool-name>` (detailed stats)
- [ ] `boxy pool logs <pool-name>` (recent events)

### 2.2 Resource State Management (Week 3-4)

**ADR Needed:** ADR-009 - Cold/Warm Resource States (already exists, needs implementation)
- Implement cold/warm resource states
- Add preheating phase
- Distinguish provision (create) vs preheat (finalize)

**Changes:**
- [ ] Add resource states: `cold`, `warming`, `warm`, `allocated`
- [ ] Implement preheating workflow
- [ ] Add `boxy pool preheat <pool-name> --count=N` command
- [ ] Update pool manager to handle new states

### 2.3 HTTP API Completeness (Week 4)

- [ ] API endpoints for pool management (scale, info)
- [ ] API endpoints for resource introspection
- [ ] API authentication/authorization (at least basic auth)
- [ ] API rate limiting
- [ ] OpenAPI/Swagger documentation

**Deliverable:** v0.2.0 - Production-ready core

---

## Phase 3: Advanced Features (Optional)

**Goal:** Add power-user features and enterprise capabilities.

### 3.1 Additional Providers

- [ ] Docker provider (container-based workspaces)
- [ ] Hyper-V provider (Windows VM workspaces)
- [ ] Remote agent support (distributed pools)

### 3.2 Advanced Hook System

**ADR Needed:** ADR-012 - Hook Extensibility
- Support for non-script hooks (HTTP webhooks, gRPC calls)
- Hook templating and parameterization
- Hook result caching

### 3.3 Observability & Operations

- [ ] Prometheus metrics endpoint
- [ ] Structured logging with levels
- [ ] Health check endpoints
- [ ] Graceful shutdown handling
- [ ] Resource usage tracking (disk, memory)

### 3.4 Multi-Tenancy & Security

- [ ] Tenant isolation in sandboxes
- [ ] Resource quotas per tenant
- [ ] Audit logging
- [ ] Secrets management integration

**Deliverable:** v0.3.0 - Enterprise features

---

## Phase 4: Ecosystem & Integration

**Goal:** Make Boxy integrate well with existing tools.

### 4.1 CI/CD Integration

- [ ] GitHub Actions integration
- [ ] GitLab CI integration
- [ ] Jenkins plugin

### 4.2 Developer Experience

- [ ] VS Code extension (connect to sandbox)
- [ ] Shell completions (bash, zsh, fish)
- [ ] TUI (terminal UI) for pool monitoring

### 4.3 Storage Backends

- [ ] PostgreSQL backend
- [ ] MySQL backend
- [ ] Redis backend (for fast ephemeral state)

**Deliverable:** v1.0.0 - Full ecosystem

---

## Decision Log

### Open Questions

1. **Sandbox vs Resource terminology**
   - Should we rename "sandbox create" to "sandbox build"?
   - Should allocation be separate from creation?
   - Current: `boxy sandbox create` does both
   - Alternative: `boxy sandbox build` + `boxy sandbox allocate`
   - **Decision needed before v0.2.0**

2. **Config file updates**
   - Should `boxy pool scale` modify the YAML file?
   - Or only affect runtime state (until restart)?
   - **Recommendation:** Runtime-only, add separate `boxy config update` command

3. **Resource recycling**
   - Should resources be destroyed after release, or recycled?
   - Current: Destroyed and reprovisioned
   - Alternative: Release → cleanup → back to pool
   - **Impacts:** ADR-009 implementation

### Deferred Features

- **Multi-region support** - Not needed until scale demands it
- **GUI dashboard** - TUI is sufficient for now
- **Kubernetes operator** - Wait for user demand
- **Plugin system** - Current provider interface is sufficient

---

## Success Metrics

### v0.1.0 (MVP)
- [ ] Zero test failures
- [ ] All core workflows documented
- [ ] At least 3 users can successfully create/use sandboxes
- [ ] Mean time to first sandbox < 5 minutes

### v0.2.0 (Production)
- [ ] 80%+ test coverage
- [ ] Can run 100+ concurrent sandboxes
- [ ] Pool operations complete in < 1 second
- [ ] API response time p95 < 100ms

### v1.0.0 (Enterprise)
- [ ] 90%+ test coverage
- [ ] Can run 1000+ concurrent sandboxes
- [ ] Zero-downtime deployments
- [ ] Multi-tenant isolation verified

---

## Timeline Estimate

- **Phase 1 (Polish):** 2 weeks
- **Phase 2 (Production):** 4 weeks
- **Phase 3 (Advanced):** 6 weeks
- **Phase 4 (Ecosystem):** 8 weeks

**Total to v1.0:** ~5 months (assuming 1 developer, part-time)

---

## How to Contribute

See individual ADRs for architectural decisions and context.

For immediate work, check `TODO.md` for session-persistent task tracking.
