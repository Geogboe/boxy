# Boxy v2 Implementation Plan

**Version:** v2.0
**Date:** 2024-11-22
**Status:** Planning
**Depends On:** v1 completion

---

## Executive Summary

This document outlines planned features for Boxy v2, building on the solid foundation established in v1. v2 focuses on developer experience, advanced scheduling, and enhanced observability.

**Key Features:**

1. 🔌 **VSCode Extension** - IDE integration for managing sandboxes
2. 📊 **Advanced Scheduling** - Capacity-aware, cost-optimized allocation
3. 🔄 **Retry Strategies** - Automatic retry with exponential backoff
4. 📈 **Enhanced Observability** - Metrics, distributed tracing, dashboards
5. 🌐 **Network Isolation** - Overlay networks (WireGuard/Headscale)
6. 🔧 **REST API** - Full HTTP API for programmatic access

---

## Table of Contents

1. [VSCode Extension](#1-vscode-extension)
2. [Advanced Scheduling](#2-advanced-scheduling)
3. [Retry Strategies](#3-retry-strategies)
4. [Enhanced Observability](#4-enhanced-observability)
5. [Network Isolation](#5-network-isolation)
6. [REST API](#6-rest-api)
7. [Implementation Priorities](#7-implementation-priorities)

---

## 1. VSCode Extension

**Purpose**: Manage Boxy sandboxes directly from VSCode for seamless development workflow.

### 1.1 Features

#### Sandbox Management

- **Create sandbox** - Right-click menu or command palette
- **List sandboxes** - Tree view in sidebar
- **Connect to sandbox** - SSH into container/VM directly from VSCode
- **Destroy sandbox** - Quick cleanup from sidebar
- **Extend duration** - Extend expiration with one click

#### Pool Visibility

- **View pools** - See available pools and stats
- **Pool health** - Visual indicators for pool status
- **Resource availability** - Real-time ready/allocated counts

#### Configuration

- **Schema validation** - Real-time YAML validation for boxy.yaml
- **Autocomplete** - IntelliSense for all config fields
- **Snippets** - Common pool/hook templates

### 1.2 UI Design

```text
BOXY EXTENSION
├── 📦 Sandboxes
│   ├── 🟢 my-dev-env (Ready)
│   │   ├── 📊 Resources: 3
│   │   ├── ⏰ Expires: 2h 15m
│   │   ├── 🔗 Connect via SSH
│   │   ├── ⏱️ Extend Duration
│   │   └── 🗑️ Destroy
│   └── 🟡 test-lab (Creating)
│       └── 📊 Resources: 0/2
├── 🏊 Pools
│   ├── win-server-2022
│   │   ├── Ready: 3
│   │   ├── Allocated: 2
│   │   └── Health: ✓
│   └── ubuntu-containers
│       ├── Ready: 5
│       ├── Allocated: 1
│       └── Health: ✓
└── ⚙️ Settings
    └── Server: https://boxy.internal:8443
```

### 1.3 Commands

```text
Boxy: Create Sandbox
Boxy: List Sandboxes
Boxy: Connect to Sandbox
Boxy: Destroy Sandbox
Boxy: Extend Sandbox Duration
Boxy: View Pool Stats
Boxy: Refresh
Boxy: Configure Server
```

### 1.4 Implementation

**Tech Stack:**

- Language: TypeScript
- Framework: VSCode Extension API
- Communication: Boxy CLI (shell out) or REST API (when available)

**Package Structure:**

```text
extensions/vscode/
├── src/
│   ├── extension.ts           # Extension entry point
│   ├── commands/
│   │   ├── sandbox.ts         # Sandbox commands
│   │   └── pool.ts            # Pool commands
│   ├── providers/
│   │   ├── sandboxTreeProvider.ts
│   │   └── poolTreeProvider.ts
│   ├── api/
│   │   └── boxyClient.ts      # Boxy API wrapper
│   └── config/
│       └── settings.ts         # Extension settings
├── package.json
├── tsconfig.json
└── README.md
```

**Configuration Schema:**

```json
{
  "boxy.serverUrl": "https://boxy.internal:8443",
  "boxy.apiToken": "<token>",
  "boxy.autoRefresh": true,
  "boxy.refreshInterval": 30
}
```

### 1.5 Distribution

- Publish to VSCode Marketplace
- Open source on GitHub
- CI/CD for automated releases

---

## 2. Advanced Scheduling

**Purpose**: Intelligent resource allocation based on capacity, cost, and performance.

### 2.1 Scheduling Strategies

#### Capacity-Aware Scheduling

Allocate from pool with most available resources:

```yaml
pools:
  - name: win-vms-pool-a
    min_ready: 10
    scheduling:
      strategy: capacity-aware
      weight: 1.0

  - name: win-vms-pool-b
    min_ready: 10
    scheduling:
      strategy: capacity-aware
      weight: 1.0
```

**Algorithm:**

```text
score = (available_resources / total_resources) * weight
select pool with highest score
```

#### Cost-Optimized Scheduling

Prefer cheaper pools:

```yaml
pools:
  - name: spot-instances
    cost_per_hour: 0.05
    scheduling:
      strategy: cost-optimized
      fallback: on-demand-instances

  - name: on-demand-instances
    cost_per_hour: 0.20
```

#### Performance-Optimized Scheduling

Prefer pools with fastest allocation times:

```yaml
pools:
  - name: preheated-pool
    preheating:
      enabled: true
      count: 10
    scheduling:
      strategy: performance-optimized
```

**Metrics:**

- Track average allocation time per pool
- Prefer pools with < 10s allocation time

### 2.2 Multi-Pool Allocation

Support allocating from multiple pools in single request:

```bash
# Allocate from ANY win-server pool
boxy sandbox create \
  --pool "win-server-*:1" \
  --strategy capacity-aware
```

### 2.3 Resource Migration

Move resources between pools (v2.1):

```bash
boxy pool migrate \
  --from win-vms-pool-a \
  --to win-vms-pool-b \
  --count 5
```

---

## 3. Retry Strategies

**Purpose**: Automatic retry with exponential backoff for transient failures.

### 3.1 Configuration

```yaml
pools:
  - name: win-test-vms

    retry:
      enabled: true
      max_attempts: 3
      backoff: exponential        # 1s, 2s, 4s, 8s
      jitter: true                # Add randomness to prevent thundering herd

      # What operations to retry
      provision: true
      warmup: true
      hooks: true
      destroy: true               # Retry destroy if fails

      # Retryable errors
      retry_on:
        - network_timeout
        - provider_busy
        - resource_exhaustion     # e.g., out of memory on host
```

### 3.2 Implementation

```go
func (m *Manager) provisionOneWithRetry(ctx context.Context) error {
    cfg := m.config.Retry

    for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
        err := m.provisionOne(ctx)
        if err == nil {
            return nil // Success
        }

        // Check if error is retryable
        if !isRetryable(err, cfg.RetryOn) {
            return err // Non-retryable error
        }

        // Calculate backoff
        backoff := calculateBackoff(attempt, cfg.Backoff, cfg.Jitter)

        m.logger.WithFields(logrus.Fields{
            "attempt": attempt + 1,
            "max":     cfg.MaxAttempts,
            "backoff": backoff,
            "error":   err,
        }).Warn("Retrying after failure")

        time.Sleep(backoff)
    }

    return fmt.Errorf("provisioning failed after %d attempts", cfg.MaxAttempts)
}
```

### 3.3 Metrics

Track retry metrics:

- `boxy_retries_total{operation="provision", result="success|failure"}`
- `boxy_retry_backoff_seconds{operation="provision"}`

---

## 4. Enhanced Observability

**Purpose**: Comprehensive metrics, tracing, and logging for production deployments.

### 4.1 Metrics (Prometheus)

**Expose metrics endpoint:**

```bash
curl http://localhost:9090/metrics

# Pool metrics
boxy_pool_resources_total{pool="win-vms",state="ready"} 5
boxy_pool_resources_total{pool="win-vms",state="allocated"} 3
boxy_pool_allocation_duration_seconds{pool="win-vms",quantile="0.99"} 2.5

# Sandbox metrics
boxy_sandboxes_total{state="ready"} 10
boxy_sandbox_creation_duration_seconds{quantile="0.95"} 30.2

# Agent metrics
boxy_agent_connected{agent="windows-host-01"} 1
boxy_agent_rpc_duration_seconds{method="Provision",quantile="0.99"} 45.1
```

### 4.2 Distributed Tracing (OpenTelemetry)

**Trace sandbox creation end-to-end:**

```text
span: sandbox.Create (30s)
  ├─ span: allocator.AllocateFromPool (2s)
  │   ├─ span: pool.GetAvailableResources (100ms)
  │   └─ span: provider.Provision (1.8s)
  │       └─ span: hyperv.CreateVM (1.7s)  # Remote agent
  ├─ span: hook.Execute[on_allocate] (25s)
  │   └─ span: powershell.Exec (24.5s)
  └─ span: database.Update (500ms)
```

**Integration:**

- Export to Jaeger or Zipkin
- Correlate across server → agent boundary
- Include trace IDs in logs

### 4.3 Structured Logging

**Enhance logging with structured fields:**

```json
{
  "timestamp": "2024-11-22T15:30:00Z",
  "level": "info",
  "msg": "provisioned resource",
  "component": "pool-manager",
  "pool": "win-vms",
  "resource_id": "res-abc123",
  "provider": "hyperv",
  "agent": "windows-host-01",
  "duration_ms": 1800,
  "trace_id": "abc123xyz789",
  "span_id": "def456"
}
```

### 4.4 Dashboards (Grafana)

**Pre-built dashboards:**

- Pool overview (resources, allocation rate, health)
- Sandbox lifecycle (creation, duration, expiration)
- Agent health (connectivity, RPC latency, errors)
- System health (CPU, memory, disk)

---

## 5. Network Isolation

**Purpose**: Isolated networks for sandbox environments with overlay networking.

### 5.1 Overlay Networks (WireGuard/Headscale)

**Configuration:**

```yaml
networking:
  overlay:
    enabled: true
    backend: wireguard
    subnet: 10.99.0.0/16

sandboxes:
  # Each sandbox gets isolated network
  - id: sb-abc123
    network:
      subnet: 10.99.1.0/24
      gateway: 10.99.1.1
```

**Features:**

- Isolated network per sandbox
- Resources in sandbox can communicate
- No cross-sandbox communication
- Optional internet access via NAT

### 5.2 Network Policies

```yaml
pools:
  - name: win-vms

    network_policy:
      internet_access: allow      # allow, deny, nat
      cross_sandbox: deny         # Prevent sandbox-to-sandbox communication
      allowed_ports:
        - 22                      # SSH
        - 3389                    # RDP
        - 5985-5986               # WinRM
```

---

## 6. REST API

**Purpose**: Full HTTP API for programmatic access and integrations.

### 6.1 API Endpoints

See [V1_IMPLEMENTATION_PLAN.md Section 9.2](V1_IMPLEMENTATION_PLAN.md#92-api-endpoint-reference) for complete API reference.

**v2 Additions:**

- WebSocket support for real-time updates
- GraphQL endpoint (optional)
- Webhook subscriptions

### 6.2 SDKs

**Official SDKs:**

- Go SDK (boxy-go)
- Python SDK (boxy-py)
- JavaScript/TypeScript SDK (boxy-js)

---

## 7. Implementation Priorities

### 7.1 Phase Breakdown

**v2.0 (First Release)**

- ✅ VSCode Extension (core features)
- ✅ Advanced Scheduling (capacity-aware)
- ✅ Retry Strategies
- ✅ Enhanced Metrics (Prometheus)

**v2.1 (Follow-up)**

- ✅ Network Isolation (WireGuard)
- ✅ Distributed Tracing
- ✅ REST API
- ✅ Official SDKs

**v2.2 (Polish)**

- ✅ Resource Migration
- ✅ Cost-optimized scheduling
- ✅ Grafana dashboards
- ✅ GraphQL API (optional)

### 7.2 Dependencies

**Requires from v1:**

- Distributed agents working
- Multi-tenancy implemented
- Pool/Sandbox peer architecture stable

---

## Success Criteria

v2 is complete when:

1. ✅ VSCode extension published to marketplace
2. ✅ Advanced scheduling working (capacity-aware, cost-optimized)
3. ✅ Retry strategies implemented and tested
4. ✅ Prometheus metrics exported
5. ✅ Distributed tracing with OpenTelemetry
6. ✅ Network isolation via WireGuard working
7. ✅ REST API documented and tested
8. ✅ At least one official SDK released (Go or Python)
9. ✅ All v2 features documented
10. ✅ No regressions from v1

---

## Future Considerations (v3+)

Features deferred to v3 or later:

- **Multi-cloud** - Resources across AWS, Azure, GCP
- **GPU workloads** - ML training, rendering farms
- **Persistent storage** - Volumes that survive resource destruction
- **Pool layering** - Build pools from other pools
- **Scheduled provisioning** - "I need 50 VMs ready by 9am tomorrow"
- **Advanced resource preemption** - Priority-based allocation

---

**Version:** v2.0
**Last Updated:** 2024-11-22
**Status:** Planning - Pending v1 Completion
