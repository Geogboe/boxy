# Boxy v1 Prerelease Implementation

**Status**: Pre-release (not production ready)
**Target**: Internal testing and validation
**Release Readiness**: v5+ (estimated)

---

## Overview

This directory contains the complete specification for Boxy v1 Prerelease. Each feature is documented in a separate file with metadata tags for prioritization and dependency tracking.

## Document Metadata Tags

Each feature document includes frontmatter with the following tags:

```yaml
---
feature: "Human-readable feature name"
status: "not-started" | "in-progress" | "done" | "blocked"
priority: "critical" | "high" | "medium" | "low"
type: "feature" | "fix" | "refactor" | "documentation"
effort: "small" | "medium" | "large"  # small=<1wk, medium=1-2wk, large=2+wk
depends_on: ["other-feature-slugs"]
enables: ["features-this-unlocks"]
testing: ["unit", "integration", "e2e", "manual"]
breaking_change: true | false
week: "1-2"  # Estimated implementation timeline
---
```

### Priority Definitions

- **critical**: Blocker for other features, core architecture
- **high**: Major feature, significant value
- **medium**: Important but not blocking
- **low**: Nice-to-have, polish

### Effort Estimates

- **small**: < 1 week (1-3 days)
- **medium**: 1-2 weeks
- **large**: 2+ weeks

---

## Implementation Phases

### Phase 1: Core Architecture (Week 1-2) 🔴 CRITICAL

| # | Feature | Priority | Effort | Status | Breaking |
| --- | --------- | ---------- | -------- | -------- | ---------- |
| 01 | [Architecture Refactor](01-architecture-refactor.md) | critical | large | not-started | no |
| 02 | [Preheating & Recycling](02-preheating-recycling.md) | critical | large | not-started | yes |
| 03 | [Terminology Updates](03-terminology-updates.md) | high | small | not-started | yes |

**Rationale**: These establish the foundation for all other features. Allocator architecture enables pool CLI, multi-tenancy, and distributed agents.

---

### Phase 2: Multi-Tenancy & CLI (Week 3-4) 🟠 HIGH

| # | Feature | Priority | Effort | Status | Breaking |
| --- | --------- | ---------- | -------- | -------- | ---------- |
| 04 | [Multi-Tenancy](04-multi-tenancy.md) | high | large | not-started | yes |
| 05 | [Base Image Validation](05-base-image-validation.md) | medium | small | not-started | no |
| 06 | [Pool CLI Commands](06-pool-cli-commands.md) | high | medium | not-started | no |

**Rationale**: Multi-tenancy is required for production use. Pool CLI makes pools first-class. Validation ensures resource quality.

**Dependencies**: Requires Phase 1 (Allocator architecture).

---

### Phase 3: Distributed & Config (Week 5-6) 🔴 CRITICAL

| # | Feature | Priority | Effort | Status | Breaking |
| --- | --------- | ---------- | -------- | -------- | ---------- |
| 07 | [Distributed Agents](07-distributed-agents.md) | critical | large | not-started | no |
| 08 | [Config Schema](08-config-schema.md) | high | medium | not-started | no |
| 11 | [Config Location](11-config-location.md) | high | small | not-started | yes |

**Rationale**: Distributed agents are essential for Hyper-V on Windows from Linux server. Config schema enables validation and IDE support. Project-specific config is correct model.

**Dependencies**: Requires Phase 1 (Provider interface).

---

### Phase 4: Developer Experience (Week 7-8) 🟡 MEDIUM

| # | Feature | Priority | Effort | Status | Breaking |
| --- | --------- | ---------- | -------- | -------- | ---------- |
| 12 | [Docker & Compose](12-docker-compose.md) | high | medium | not-started | no |
| 09 | [CLI/API Schemas](09-cli-api-schemas.md) | medium | small | not-started | no |
| 10 | [Debugging Guide](10-debugging-guide.md) | medium | small | not-started | no |

**Rationale**: Docker deployment is standard practice. Complete schemas prevent regressions. Debugging guide reduces support burden.

**Dependencies**: Can be done in parallel with Phase 3.

---

### Phase 5: Polish & Release Prep (Week 9) 🟢 LOW

| # | Feature | Priority | Effort | Status | Breaking |
| --- | --------- | ---------- | -------- | -------- | ---------- |
| 13 | [Documentation Updates](13-documentation-updates.md) | low | small | not-started | no |
| -- | [Testing Strategy](testing-strategy.md) | critical | -- | not-started | no |
| -- | [Migration Guide](migration-guide.md) | high | small | not-started | no |

**Rationale**: Final consistency pass. Complete test coverage. Migration path for users.

**Dependencies**: All previous phases.

---

## Dependency Graph

```text
01-architecture-refactor (foundation)
├── 02-preheating-recycling
├── 04-multi-tenancy
├── 06-pool-cli-commands
└── 07-distributed-agents
    └── 12-docker-compose

03-terminology-updates (independent)

05-base-image-validation (independent)

08-config-schema (independent)

11-config-location (independent)

09-cli-api-schemas (after all features)

10-debugging-guide (after all features)

13-documentation-updates (final pass)
```

---

## Quick Start: What to Implement First

### Critical Path (must be done in order)

1. **01-architecture-refactor** - Allocator + peer architecture
2. **07-distributed-agents** - gRPC/mTLS for Windows agents
3. **02-preheating-recycling** - Resource efficiency
4. **04-multi-tenancy** - Production readiness

### Parallel Workstreams

- **Config**: 08, 11 (can be done anytime)
- **CLI/Docs**: 06, 09, 10, 13 (after core features)
- **Validation**: 05 (after pools stable)
- **Docker**: 12 (for testing throughout)

---

## Success Criteria

v1 Prerelease is complete when:

- ✅ All critical and high priority features implemented
- ✅ Integration tests pass with Docker provider
- ✅ E2E tests pass with stubbed Hyper-V provider
- ✅ Distributed agent communication works (Linux server → Windows agent)
- ✅ No functional regressions from MVP
- ✅ All breaking changes documented in migration guide
- ✅ Test coverage > 80% for core components
- ✅ Primary use case (quick testing) works end-to-end

---

## Breaking Changes Summary

| Feature | Breaking Change | Impact |
| --------- | ---------------- | -------- |
| 02-preheating-recycling | New resource states | Config updates required |
| 03-terminology-updates | Hook names changed | Config migration needed |
| 04-multi-tenancy | Auth required | API tokens needed |
| 11-config-location | Config path changed | Update deployment scripts |

**Note**: Since v1 is pre-release, breaking changes are acceptable. No production deployments exist.

---

## Testing Strategy

See [testing-strategy.md](testing-strategy.md) for comprehensive TDD approach with:

- Stub/mock harness for Hyper-V testing
- Integration tests with real Docker provider
- E2E smoke tests for critical paths
- Regression tests for MVP functionality

---

## Files in This Directory

| File | Description | Priority | Effort |
| ------ | ------------- | ---------- | -------- |
| 01-architecture-refactor.md | Allocator + peer design | critical | large |
| 02-preheating-recycling.md | Cold/warm resources | critical | large |
| 03-terminology-updates.md | Hook naming consistency | high | small |
| 04-multi-tenancy.md | Users, teams, tokens, quotas | high | large |
| 05-base-image-validation.md | Resource validation system | medium | small |
| 06-pool-cli-commands.md | Pool management commands | high | medium |
| 07-distributed-agents.md | gRPC/mTLS remote providers | critical | large |
| 08-config-schema.md | YAML schema + validation | high | medium |
| 09-cli-api-schemas.md | Complete interface docs | medium | small |
| 10-debugging-guide.md | Troubleshooting guide | medium | small |
| 11-config-location.md | Project-specific config | high | small |
| 12-docker-compose.md | Container deployment | high | medium |
| 13-documentation-updates.md | Consistency review | low | small |
| testing-strategy.md | TDD approach with stubs | critical | -- |
| migration-guide.md | Breaking changes guide | high | small |

---

**Last Updated**: 2025-11-23
**Version**: v1-prerelease
**Next Review**: After Phase 1 completion
