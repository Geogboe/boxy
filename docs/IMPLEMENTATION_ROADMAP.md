# Implementation Roadmap

This document provides a phased implementation plan for Boxy with hook-based provisioning and distributed agent architecture. For the broader project vision and other phases, please refer to the [Boxy Development Roadmap](../ROADMAP.md).

## Overview

**Goal**:

1. Core pool management with hook-based provisioning
2. Distributed agent architecture for remote providers
3. Token-based secure agent registration

**Success Criteria**:

- [ ] Server can manage multiple remote agents
- [ ] Agents authenticate with mTLS
- [ ] Providers transparently route to local or remote
- [ ] All tests pass (unit, integration, E2E)
- [ ] Security audit completed
- [ ] Documentation complete

## Phase 1: Core Hook System (Days 1-5)

### Day 1-2: Provider Interface with Hooks

**Tasks**:

1. Update Provider interface with Execute() method
2. Add ResourceUpdate for generic updates
3. Create hook execution framework
4. Add timeout and retry logic

**Files**:

- `pkg/provider/provider.go` - Provider interface
- `internal/hooks/executor.go` - Hook execution engine
- `internal/hooks/types.go` - Hook types and config

**Tests**:

- Unit tests for hook executor
- Mock provider with Execute()
- Timeout and retry behavior

### Day 3-4: Pool Manager Hook Integration

**Tasks**:

1. Add hook execution to pool provisioning
2. Implement async allocation with status tracking
3. Add finalization (after_provision) hooks
4. Add personalization (before_allocate) hooks

**Files**:

- `internal/core/pool/manager.go` - Add hook execution
- `internal/core/pool/hooks.go` - Hook management
- `internal/core/resource/types.go` - Add provisioning status

**Tests**:

- Integration tests with Docker provider
- Test hook failures and retries
- Test async allocation flow

### Day 5: CLI Updates for Async

**Tasks**:

1. Update `boxy sandbox create` to wait for provisioning
2. Add progress indicators
3. Add `--no-wait` flag for async mode
4. Add status polling

**Files**:

- `cmd/boxy/commands/sandbox.go`
- Add spinner/progress library

**Tests**:

- Manual testing with Docker
- Test wait vs no-wait modes

## Phase 2: Hyper-V Stub & Testing (Days 6-8)

### Day 1: Protocol Buffers & Code Generation

**Tasks**:

1. ✅ Define Protocol Buffers schema (completed - `pkg/provider/proto/provider.proto`)
2. Generate Go code from proto files
3. Add proto generation to build process
4. Test proto serialization/deserialization

**Deliverables**:

- [ ] Generated gRPC code in `pkg/provider/proto/`
- [ ] Makefile target: `make proto`
- [ ] Unit tests for proto conversion functions

**Implementation**:

```bash
# Install protoc compiler and Go plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Add to Makefile
proto:
 protoc --go_out=. --go_opt=paths=source_relative \
        --go-grpc_out=. --go-grpc_opt=paths=source_relative \
        pkg/provider/proto/provider.proto

# Generate code
make proto
```

**Tests**:

```go
// pkg/provider/proto/proto_test.go
func TestResourceSpecConversion(t *testing.T) {
    spec := resource.ResourceSpec{
        Type: resource.ResourceTypeContainer,
        Image: "ubuntu:22.04",
        CPUs: 2,
        MemoryMB: 1024,
    }

    proto := specToProto(spec)
    assert.Equal(t, "container", proto.Type)
    assert.Equal(t, "ubuntu:22.04", proto.Image)

    back := protoToSpec(proto)
    assert.Equal(t, spec.Type, back.Type)
    assert.Equal(t, spec.Image, back.Image)
}
```

### Day 2: RemoteProvider Implementation

**Tasks**:

1. Create `pkg/provider/remote` package
2. Implement RemoteProvider struct
3. Implement Provider interface methods
4. Add connection management (dial, close, reconnect)
5. Implement proto conversion helpers

**Deliverables**:

- [ ] `pkg/provider/remote/remote.go`
- [ ] `pkg/provider/remote/convert.go` (proto conversions)
- [ ] `pkg/provider/remote/remote_test.go`

**Code Structure**:

```text
pkg/provider/remote/
├── remote.go       # RemoteProvider implementation
├── convert.go      # Proto <-> domain conversions
├── remote_test.go  # Unit tests
└── mock_test.go    # Mock gRPC client for tests
```

**Tests**:

- Unit tests with mock gRPC client
- Test all Provider interface methods
- Test connection error handling
- Test proto conversion accuracy

### Day 3: Agent Server Implementation

**Tasks**:

1. Create `internal/agent` package
2. Implement Agent Server (gRPC server)
3. Implement ProviderService RPCs
4. Implement authorization logic
5. Add logging and metrics

**Deliverables**:

- [ ] `internal/agent/server.go`
- [ ] `internal/agent/authorization.go`
- [ ] `internal/agent/server_test.go`

**Code Structure**:

```text
internal/agent/
├── server.go          # gRPC server implementation
├── authorization.go   # mTLS auth and provider authz
├── registration.go    # Registration client (Phase 3)
├── server_test.go     # Unit tests
└── testutil.go        # Test helpers
```

**Tests**:

- Unit tests with embedded provider
- Test each RPC method
- Test authorization checks
- Test error handling

## Phase 2: Security (Week 1, Days 4-5)

### Day 4: Certificate Management

**Tasks**:

1. Create `internal/cert` package
2. Implement CA initialization
3. Implement certificate issuance
4. Implement TLS config loading
5. Add certificate validation

**Deliverables**:

- [ ] `internal/cert/ca.go`
- [ ] `internal/cert/cert.go`
- [ ] `internal/cert/tls.go`
- [ ] `internal/cert/cert_test.go`

**Code Structure**:

```text
internal/cert/
├── ca.go           # CA operations (init, issue, revoke)
├── cert.go         # Certificate helpers
├── tls.go          # TLS config creation
├── cert_test.go    # Unit tests
└── testdata/       # Test certificates
```

**Tests**:

- Test CA initialization
- Test certificate issuance
- Test certificate validation
- Test TLS config creation

### Day 5: CLI Commands for Certificate Management

**Tasks**:

1. Add `boxy admin` command group
2. Implement `boxy admin init-ca`
3. Implement `boxy admin issue-cert`
4. Add certificate verification command
5. Add validation and error handling

**Deliverables**:

- [ ] `cmd/boxy/commands/admin.go`
- [ ] `cmd/boxy/commands/admin_init_ca.go`
- [ ] `cmd/boxy/commands/admin_issue_cert.go`
- [ ] Integration tests for CLI commands

**CLI Commands**:

```bash
# Initialize CA
boxy admin init-ca --output /etc/boxy/ca

# Issue server certificate
boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --cert-type server \
  --common-name boxy-server \
  --dns-names boxy-server.internal \
  --output /etc/boxy/server

# Issue agent certificate
boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --cert-type agent \
  --agent-id windows-host-01 \
  --output /etc/boxy/agents/windows-01

# Verify certificate
boxy admin verify-cert \
  --cert /etc/boxy/agents/windows-01-cert.pem \
  --ca /etc/boxy/ca/ca-cert.pem
```

**Tests**:

- Integration tests for each command
- Test certificate generation
- Test validation logic
- Test error cases (missing files, invalid params)

## Phase 3: Agent Mode (Week 2, Days 6-10)

### Day 6: Agent Registration

**Tasks**:

1. Implement registration client
2. Implement heartbeat mechanism
3. Add agent discovery to server
4. Create agent registry in server

**Deliverables**:

- [ ] `internal/agent/registration.go` (client-side)
- [ ] `internal/server/agent_registry.go` (server-side)
- [ ] `internal/server/agent_service.go` (AgentService impl)

**Agent Lifecycle**:

```text
Agent Start
    ↓
Load Certificate
    ↓
Connect to Server (mTLS)
    ↓
Register (send providers list)
    ↓
Start Heartbeat Loop (every 30s)
    ↓
Start Provider Server (gRPC)
    ↓
Ready to Serve Requests
```

**Tests**:

- Test registration flow
- Test heartbeat mechanism
- Test agent registry operations
- Test connection failures

### Day 7: Agent Command Implementation

**Tasks**:

1. Add `boxy agent` command group
2. Implement `boxy agent serve`
3. Add agent configuration
4. Implement graceful shutdown
5. Add status reporting

**Deliverables**:

- [ ] `cmd/boxy/commands/agent.go`
- [ ] `cmd/boxy/commands/agent_serve.go`
- [ ] Updated configuration schema for agents

**Configuration**:

```yaml
# boxy.yaml (agent mode)
agent:
  id: windows-host-01
  server_url: https://boxy-server:8443
  listen_address: 0.0.0.0:8444

  # Providers to expose
  providers:
    - hyperv

  # TLS configuration
  tls:
    cert_file: /etc/boxy/agent-cert.pem
    key_file: /etc/boxy/agent-key.pem
    ca_file: /etc/boxy/ca-cert.pem

  # Registration
  registration:
    enabled: true
    heartbeat_interval: 30s
```

**Tests**:

- E2E test starting agent
- Test registration on startup
- Test graceful shutdown
- Test configuration loading

### Day 8-9: Health Monitoring & Failover

**Tasks**:

1. Implement agent health tracking
2. Add agent status endpoints
3. Implement connection retry logic
4. Add circuit breakers
5. Implement basic failover (manual)

**Deliverables**:

- [ ] `internal/server/agent_health.go`
- [ ] `pkg/provider/remote/reconnect.go`
- [ ] `pkg/provider/remote/circuit_breaker.go`

**Health Tracking**:

```go
type AgentHealth struct {
    AgentID           string
    Status            AgentStatus  // healthy, degraded, down
    LastHeartbeat     time.Time
    LastError         error
    ConsecutiveFailures int
    AvgLatency        time.Duration
}
```

**Tests**:

- Test health status transitions
- Test connection retry with backoff
- Test circuit breaker opens/closes
- Test failover to backup agent

### Day 10: Integration Testing

**Tasks**:

1. Set up integration test environment
2. Create Docker Compose for multi-node testing
3. Write integration tests for full flow
4. Test multi-agent scenarios
5. Test failure scenarios

**Deliverables**:

- [ ] `tests/integration/agent_integration_test.go`
- [ ] `tests/integration/docker-compose.yml`
- [ ] Test documentation

**Test Scenarios**:

- Server + 1 agent (Docker provider)
- Server + 2 agents (different providers)
- Agent disconnect/reconnect
- Agent crash recovery
- Certificate rotation

## Phase 4: Server Integration (Week 2, Days 11-12)

### Day 11: Configuration Updates

**Tasks**:

1. Update configuration schema for agents
2. Implement agent configuration parsing
3. Add backend_agent field to pool config
4. Update pool manager to use remote providers
5. Implement provider routing logic

**Deliverables**:

- [ ] Updated `internal/config/config.go`
- [ ] `internal/config/agent_config.go`
- [ ] Updated pool configuration

**Configuration Schema**:

```yaml
# Server configuration
server:
  mode: server
  listen_address: 0.0.0.0:8443
  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca-cert.pem
    client_auth: require

# Agent definitions
agents:
  - id: windows-host-01
    address: windows-host-01.internal:8444
    providers:
      - hyperv
    max_resources: 50

  - id: docker-host-01
    address: docker-host-01.internal:8444
    providers:
      - docker
    max_resources: 100

# Pool configuration (updated)
pools:
  - name: win-server-vms
    type: vm
    backend: hyperv
    backend_agent: windows-host-01  # NEW: route to specific agent
    image: win-server-2022-template
    min_ready: 3
    max_total: 10

  - name: ubuntu-containers
    type: container
    backend: docker
    backend_agent: docker-host-01  # NEW: route to specific agent
    image: ubuntu:22.04
    min_ready: 5
    max_total: 20
```

**Tests**:

- Test configuration parsing
- Test validation (agent exists, authorized providers)
- Test provider routing

### Day 12: Provider Factory & Registry Updates

**Tasks**:

1. Create RemoteProvider factory
2. Update provider registry initialization
3. Implement provider routing
4. Add connection pooling for remote providers
5. Test mixed local/remote providers

**Deliverables**:

- [ ] `pkg/provider/factory.go`
- [ ] Updated `cmd/boxy/commands/serve.go`
- [ ] Integration tests

**Provider Factory**:

```go
func NewProvider(cfg ProviderConfig, agentRegistry *AgentRegistry) (provider.Provider, error) {
    if cfg.AgentID == "" {
        // Local provider
        return newLocalProvider(cfg.Backend)
    }

    // Remote provider
    agent := agentRegistry.Get(cfg.AgentID)
    if agent == nil {
        return nil, fmt.Errorf("agent not found: %s", cfg.AgentID)
    }

    return remote.NewRemoteProvider(
        cfg.Backend,
        cfg.Type,
        agent.ID,
        agent.Address,
        agent.TLSConfig,
    )
}
```

**Tests**:

- Test local provider creation
- Test remote provider creation
- Test provider routing based on config
- Test mixed pools (some local, some remote)

## Phase 5: Observability (Week 3, Days 13-15)

### Day 13: Metrics

**Tasks**:

1. Add Prometheus metrics
2. Instrument gRPC calls
3. Add agent health metrics
4. Add resource operation metrics
5. Create Grafana dashboards

**Deliverables**:

- [ ] `internal/metrics/metrics.go`
- [ ] Prometheus metrics exported on `:9090/metrics`
- [ ] Grafana dashboard JSON

**Metrics**:

```go
// gRPC metrics
grpc_server_requests_total{agent_id, method, status}
grpc_server_request_duration_seconds{agent_id, method}

// Agent health
boxy_agent_status{agent_id, status}
boxy_agent_last_heartbeat_seconds{agent_id}

// Resources
boxy_resources_total{agent_id, provider, state}
boxy_provision_duration_seconds{agent_id, provider}
```

### Day 14: Distributed Tracing

**Tasks**:

1. Add OpenTelemetry support
2. Instrument gRPC with tracing
3. Add trace context propagation
4. Add Jaeger exporter
5. Test distributed traces

**Deliverables**:

- [ ] `internal/tracing/tracing.go`
- [ ] Jaeger integration
- [ ] Example traces

**Tracing**:

- Trace provision request from CLI -> Server -> Agent -> Provider
- Include span metadata (agent_id, provider, resource_id)
- Link related operations (provision -> health check -> destroy)

### Day 15: Logging & Audit

**Tasks**:

1. Implement structured audit logging
2. Add correlation IDs to all requests
3. Log all security-relevant events
4. Add log aggregation configuration
5. Create log retention policy

**Deliverables**:

- [ ] `internal/audit/audit.go`
- [ ] Log aggregation config (ELK or Loki)
- [ ] Audit event documentation

**Audit Events**:

- Agent registration (success/failure)
- Authentication failures
- Authorization denials
- Resource provisioning
- Credential access
- Configuration changes

## Phase 6: Production Readiness (Week 3, Days 16-21)

### Day 16-17: Resilience Features

**Tasks**:

1. Implement retry with exponential backoff
2. Add timeout configuration
3. Implement circuit breakers
4. Add connection pooling
5. Implement rate limiting
6. Add request queuing

**Deliverables**:

- [ ] `pkg/provider/remote/resilience.go`
- [ ] Configuration for timeouts/retries
- [ ] Stress tests

**Features**:

```go
// Retry configuration
type RetryConfig struct {
    MaxAttempts     int
    InitialBackoff  time.Duration
    MaxBackoff      time.Duration
    BackoffMultiplier float64
}

// Circuit breaker configuration
type CircuitBreakerConfig struct {
    MaxFailures      int
    Timeout          time.Duration
    ResetTimeout     time.Duration
}

// Rate limiter configuration
type RateLimitConfig struct {
    RequestsPerSecond int
    Burst             int
}
```

**Tests**:

- Test retry on transient failures
- Test timeout enforcement
- Test circuit breaker state transitions
- Test rate limiting

### Day 18-19: Stress Testing

**Tasks**:

1. Create stress test scenarios
2. Test with high concurrency
3. Test with agent failures
4. Test with network issues
5. Identify and fix bottlenecks

**Deliverables**:

- [ ] `tests/stress/distributed_stress_test.go`
- [ ] Performance benchmarks
- [ ] Scalability report

**Test Scenarios**:

- 100 concurrent provision requests
- 10 agents with 1000 resources total
- Agent restart during provisioning
- Network latency simulation
- Resource leak detection

**Benchmarks**:

```bash
# Provision throughput
go test -bench=BenchmarkProvision -benchtime=30s

# Memory usage under load
go test -bench=BenchmarkMemory -benchmem

# Latency percentiles
go test -bench=BenchmarkLatency -cpuprofile=cpu.prof
```

### Day 20: Security Audit

**Tasks**:

1. Review authentication implementation
2. Review authorization logic
3. Test certificate validation
4. Test credential encryption
5. Penetration testing
6. Fix identified vulnerabilities

**Deliverables**:

- [ ] Security audit report
- [ ] Vulnerability fixes
- [ ] Updated security documentation

**Audit Checklist**:

- [ ] mTLS properly configured
- [ ] Certificate validation works
- [ ] Authorization checks enforced
- [ ] Credentials encrypted at rest and in transit
- [ ] No credential logging
- [ ] Rate limiting effective
- [ ] SQL injection prevented (if using raw SQL)
- [ ] Input validation comprehensive
- [ ] Error messages don't leak info
- [ ] Audit logging complete

### Day 21: Documentation & Deployment Guide

**Tasks**:

1. Update README with distributed mode
2. Create deployment guide
3. Create operations runbook
4. Create troubleshooting guide
5. Create upgrade guide
6. Record demo video

**Deliverables**:

- [ ] Updated README.md
- [ ] `docs/DEPLOYMENT.md`
- [ ] `docs/OPERATIONS.md`
- [ ] `docs/TROUBLESHOOTING.md`
- [ ] `docs/UPGRADE.md`

## Definition of Done

### Code Complete

- [ ] All code written and reviewed
- [ ] No TODOs or FIXMEs in main branch
- [ ] Code follows Go best practices
- [ ] All linters pass

### Testing Complete

- [ ] All unit tests pass (>80% coverage)
- [ ] All integration tests pass
- [ ] All E2E tests pass
- [ ] Stress tests completed
- [ ] No memory leaks detected

### Security Complete

- [ ] Security audit completed
- [ ] No high/critical vulnerabilities
- [ ] All credentials encrypted
- [ ] Audit logging implemented
- [ ] Certificate management documented

### Documentation Complete

- [ ] README updated
- [ ] Architecture docs complete
- [ ] API documentation complete
- [ ] Deployment guide written
- [ ] Operations runbook created
- [ ] Troubleshooting guide created

### Production Ready

- [ ] Metrics and monitoring configured
- [ ] Distributed tracing working
- [ ] Graceful shutdown implemented
- [ ] Backup/restore tested
- [ ] Disaster recovery plan documented
- [ ] Performance benchmarks established

## Risk Management

### Technical Risks

| Risk | Impact | Likelihood | Mitigation |
| ------ | -------- | ------------ | ------------ |
| gRPC compatibility issues | High | Low | Use stable gRPC versions, extensive testing |
| Certificate management complexity | Medium | Medium | Comprehensive docs, automation |
| Network latency issues | Medium | High | Connection pooling, caching, benchmarking |
| Memory leaks with long-lived connections | High | Medium | Stress testing, profiling, connection limits |
| Agent crash during provisioning | High | Medium | Idempotent operations, state recovery |

### Operational Risks

| Risk | Impact | Likelihood | Mitigation |
| ------ | -------- | ------------ | ------------ |
| Certificate expiration | High | Medium | Automated renewal, monitoring, alerts |
| Agent misconfiguration | Medium | High | Validation, clear error messages, examples |
| Network partition | High | Low | Circuit breakers, failover, monitoring |
| CA key compromise | Critical | Low | HSM storage, access controls, rotation plan |

## Success Metrics

### Performance

- Provision latency <2s (local) or <5s (remote)
- Support 1000+ active resources
- Support 10+ agents
- Handle 100 concurrent requests

### Reliability

- 99.9% uptime
- <0.1% failed provisions
- Automatic recovery from transient failures
- No data loss on agent crash

### Security

- Zero credential leaks
- All communication encrypted
- All operations audited
- Certificate rotation automated

### Usability

- Clear documentation
- Simple deployment process
- Helpful error messages
- Easy troubleshooting

## Appendix: Quick Commands

```bash
# Generate proto code
make proto

# Run all tests
make test

# Run integration tests
make test-integration

# Run E2E tests
make test-e2e

# Run stress tests
make test-stress

# Build binary
make build

# Initialize CA
boxy admin init-ca --output /etc/boxy/ca

# Issue certificates
boxy admin issue-cert --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --agent-id test-agent \
  --output /etc/boxy/agents/test-agent

# Start server
boxy serve --config /etc/boxy/boxy.yaml

# Start agent
boxy agent serve --config /etc/boxy/agent.yaml

# View metrics
curl http://localhost:9090/metrics

# View agent status
boxy admin agent list
boxy admin agent status windows-host-01
```

## References

- [ADR-004: Distributed Agent Architecture](decisions/adr-004-distributed-agent-architecture.md)
- [Implementation Guide](architecture/distributed-agent-implementation.md)
- [Security Guide](architecture/security-guide.md)
- [Protocol Buffers](provider/proto/provider.proto)