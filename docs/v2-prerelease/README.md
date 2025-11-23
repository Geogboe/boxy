# Boxy v2 Prerelease Implementation

**Status**: Pre-release (not production ready)
**Depends On**: v1 completion
**Target**: Internal testing and validation
**Release Readiness**: v5+ (estimated)

---

## Overview

v2 builds on v1's solid foundation with focus on developer experience, advanced scheduling, and enhanced observability.

---

## Features

### Phase 1: Developer Experience (Week 1-3) 🟠 HIGH

| # | Feature | Priority | Effort | Status | Breaking |
|---|---------|----------|--------|--------|----------|
| 01 | [VSCode Extension](01-vscode-extension.md) | high | large | not-started | no |
| 06 | [REST API](06-rest-api.md) | high | medium | not-started | no |

### Phase 2: Advanced Features (Week 4-6) 🟡 MEDIUM

| # | Feature | Priority | Effort | Status | Breaking |
|---|---------|----------|--------|--------|----------|
| 02 | [Advanced Scheduling](02-advanced-scheduling.md) | medium | large | not-started | no |
| 03 | [Retry Strategies](03-retry-strategies.md) | medium | medium | not-started | no |
| 04 | [Enhanced Observability](04-enhanced-observability.md) | medium | large | not-started | no |

### Phase 3: Network Isolation (Week 7-8) 🟢 LOW

| # | Feature | Priority | Effort | Status | Breaking |
|---|---------|----------|--------|--------|----------|
| 05 | [Network Isolation](05-network-isolation.md) | low | large | not-started | no |

---

## Key Features Summary

### 01: VSCode Extension
IDE integration for managing sandboxes directly from VSCode.

### 02: Advanced Scheduling
Capacity-aware, cost-optimized resource allocation across pools.

### 03: Retry Strategies
Automatic retry with exponential backoff for transient failures.

### 04: Enhanced Observability
Metrics (Prometheus), distributed tracing (Jaeger), dashboards (Grafana).

### 05: Network Isolation
Overlay networks (WireGuard/Headscale) for sandbox isolation.

### 06: REST API
Full HTTP API for programmatic access (complements CLI).

---

## Success Criteria

v2 Prerelease is complete when:

- ✅ VSCode extension works with v1 server
- ✅ Advanced scheduling allocates optimally
- ✅ Retry strategies handle transient failures
- ✅ Metrics exported to Prometheus
- ✅ REST API complete and documented
- ✅ Network isolation works for multi-resource sandboxes
- ✅ All features tested and documented

---

## Dependencies

**Requires v1 completion:**
- Architecture refactor (Allocator)
- Distributed agents (gRPC/mTLS)
- Multi-tenancy (API tokens)
- Pool CLI commands

---

## Files in This Directory

| File | Description | Priority | Effort |
|------|-------------|----------|--------|
| 01-vscode-extension.md | IDE integration | high | large |
| 02-advanced-scheduling.md | Smart allocation | medium | large |
| 03-retry-strategies.md | Fault tolerance | medium | medium |
| 04-enhanced-observability.md | Metrics & tracing | medium | large |
| 05-network-isolation.md | Overlay networks | low | large |
| 06-rest-api.md | HTTP API | high | medium |

---

**Last Updated**: 2025-11-23
**Version**: v2-prerelease
**Next Review**: After v1 completion
