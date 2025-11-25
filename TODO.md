# Boxy TODO - Session Persistent

This file tracks work in progress and blockers. Updated continuously during development.

## 🔥 Current Session (2025-11-25)

### Completed This Session
- [x] Fixed connect script generation bug (AllocateArtifacts path issue)
- [x] Enhanced connect script with deactivate function and virtualenv-like UX
- [x] Integrated AllocateArtifacts into allocation flow (added expiresAt parameter)
- [x] Fixed allocator test stub signature
- [x] Fixed connect script panic with short sandbox IDs
- [x] Updated all hook names in tests (AfterProvision→OnProvision, BeforeAllocate→OnAllocate)
- [x] Updated all Allocate() calls in integration/e2e tests to include expiresAt parameter
- [x] Created ROADMAP.md with phased development plan
- [x] Created ADR-010 for E2E testing strategy
- [x] Created TODO.md for session-persistent tracking

### In Progress
- [ ] Verify all tests pass (some integration/e2e tests may need Docker/time-consuming)
- [ ] **NEXT:** Improve CLI output to show `source connect.sh` instructions

### Blockers / Decisions Needed
- [ ] **CLI UX Question:** Should we show the `source` command in sandbox create output?
  - Current: Shows "Connect script: /path/to/connect.sh"
  - Proposed: Show explicit `source /path/to/connect.sh` with instructions
  - Decision: ?

- [ ] **Sandbox naming question:** Keep "sandbox create" or change to "sandbox build"?
  - User mentioned: "I wonder if a sandbox should be built rather than created"
  - Current: `boxy sandbox create` (allocates immediately)
  - Alternative: `boxy sandbox build` + `boxy sandbox allocate <id>` (two-step)
  - Decision: DEFER - current UX works fine, one-step is simpler for MVP

---

## 📋 Phase 1: Polish & Stability

### Immediate (This Week)

#### Fix Tests
- [ ] Update `internal/core/allocator/allocator_test.go`
  - Change `stubPool.Allocate(ctx, sandboxID)` → `Allocate(ctx, sandboxID, expiresAt)`
- [ ] Run `go test ./...` and fix any other failures
- [ ] Verify all tests pass

#### CLI UX Improvements
- [ ] Update `cmd/boxy/commands/sandbox.go:printConnections()`
  - Add clear "How to use" section
  - Show: `source /tmp/boxy-scratch/.../connect.sh`
  - Mention deactivate function
- [ ] Add `--help` examples to sandbox commands
- [ ] Consider: Add `boxy sandbox connect <id>` command that just prints source command?

#### Documentation
- [ ] Write `docs/quickstart.md`
  - Installation
  - First sandbox creation
  - Using the connect script
  - Destroying sandboxes
- [ ] Update `README.md` with clearer overview
- [ ] Document connect script behavior

### E2E Testing (Next)

#### Framework Setup
- [ ] Create `tests/e2e/framework/` package
  - Test harness for starting/stopping boxy
  - Helpers for sandbox lifecycle
  - Assertion utilities
- [ ] Decide: In-process testing or spawn boxy binary?
  - In-process: Faster, better debugging
  - Binary: More realistic, tests actual CLI
  - **Recommendation:** Start with in-process, add binary tests later

#### Test Scenarios
- [ ] `e2e/sandbox_lifecycle_test.go`
  - Create sandbox
  - Verify connect script exists
  - Verify workspace directory
  - Destroy sandbox
  - Verify cleanup

- [ ] `e2e/pool_management_test.go`
  - Verify min_ready resources provisioned
  - Allocate resource
  - Verify pool replenishes
  - Verify max_total respected

- [ ] `e2e/concurrent_test.go`
  - Create 10 sandboxes concurrently
  - Verify all succeed
  - No resource conflicts

- [ ] `e2e/expiration_test.go`
  - Create sandbox with short TTL (10s)
  - Wait for expiration
  - Verify automatic cleanup

- [ ] `e2e/hooks_test.go`
  - Configure on_provision hook
  - Configure on_allocate hook
  - Verify hooks execute
  - Verify hook failures handled

---

## 📋 Phase 2: Essential Features (Future)

### Pool Management Commands
- [ ] Write ADR-011 - Runtime Pool Scaling
  - Decide: Modify config file or runtime-only?
  - Decide: How to handle scale-down?
  - Decide: Autoscaling support?

- [ ] Implement `boxy pool scale`
- [ ] Implement `boxy pool info`
- [ ] Add HTTP API endpoints

### Resource States (ADR-009)
- [ ] Implement cold/warm states
- [ ] Implement preheating
- [ ] Add `boxy pool preheat` command

---

## 🐛 Known Issues

### High Priority
- [ ] Tests broken after allocator signature change (blocking CI)

### Medium Priority
- [ ] CLI doesn't show clear usage instructions after sandbox creation
- [ ] No way to list active sandboxes with their connect scripts
- [ ] Pool status doesn't show resource health details

### Low Priority
- [ ] Logging is too verbose by default
- [ ] Error messages could be more helpful
- [ ] No shell completions

---

## 💡 Ideas / Future Considerations

### UX Improvements
- [ ] `boxy sandbox enter <id>` - Auto-source connect script in new shell
- [ ] `boxy sandbox exec <id> -- <command>` - Run command in sandbox context
- [ ] TUI for monitoring pools/sandboxes

### Features
- [ ] Resource usage tracking (disk, memory per sandbox)
- [ ] Sandbox templates (pre-configured environments)
- [ ] Snapshot/restore sandbox state

### Operations
- [ ] Prometheus metrics
- [ ] Health check improvements
- [ ] Graceful shutdown
- [ ] Database migrations

---

## 🎯 Current Focus

**Goal:** Get to v0.1.0 - Stable MVP
- Fix tests
- Polish CLI UX
- Write E2E tests
- Basic documentation

**Timeline:** ~1-2 weeks

**Next Session Priorities:**
1. Fix allocator tests (15 min)
2. Improve CLI output (30 min)
3. Start E2E test framework (1-2 hours)

---

## 📝 Notes

### Recent Changes
- 2025-11-25: Fixed connect script generation, added deactivate function
- 2025-11-25: Implemented ADR-008 (hook terminology changes)
- 2025-11-25: Fixed config loading bug (missing mapstructure tags)
- 2025-11-25: Fixed health check bug (wrong path reconstruction)

### Architecture Decisions Made
- Scratch provider doesn't support Exec() - it's filesystem-only
- Connect script should be sourced, not executed
- AllocateArtifacts is provider-specific (type assertion pattern)
- expiresAt passed through full allocation chain

### Open Questions
- Should sandbox creation be two-step (build + allocate)?
- Should pool scale modify config or be runtime-only?
- How should resource recycling work?
