# Distributed Agent Architecture - Executive Summary

## Problem Statement

Boxy currently runs all providers embedded in a single process on the same host. This creates limitations:

- **Platform Constraints**: Linux host cannot manage Hyper-V (Windows-only)
- **Resource Isolation**: All providers share same host resources
- **Scalability**: Cannot distribute load across multiple hosts
- **Security**: No isolation between provider backends

**Example Limitation**: A Linux server running Boxy cannot provision Windows VMs on a Hyper-V host.

## Proposed Solution

**Distributed Agent Architecture** with transparent remote provider proxying:

```text
Server (Linux)                    Agent (Windows)
┌─────────────────┐              ┌──────────────────┐
│  Pool Manager   │              │  Hyper-V         │
│       ↓         │   gRPC/TLS   │  Provider        │
│  Docker (local) │              │  (local to       │
│  Hyper-V (remote)──────────────→  agent)          │
└─────────────────┘              └──────────────────┘
```

### Key Features

1. **Single Binary**: `boxy` runs as server, agent, or both
2. **Transparent Proxying**: Remote providers look identical to local ones
3. **Secure by Default**: mTLS authentication required
4. **Backwards Compatible**: Existing deployments work unchanged

## Architecture Components

### 1. RemoteProvider (Client Proxy)

- Implements standard `Provider` interface
- Translates calls to gRPC requests
- Manages connection lifecycle
- Handles retries and circuit breaking

**File**: `pkg/provider/remote/remote.go`

### 2. Agent Server (gRPC Server)

- Exposes local providers via gRPC
- Enforces mTLS authentication
- Authorizes provider access
- Reports health and stats

**File**: `internal/agent/server.go`

### 3. Certificate Management

- CA initialization and certificate issuance
- mTLS configuration
- Certificate rotation
- Revocation support

**File**: `internal/cert/`

### 4. Protocol Buffers

- Type-safe RPC definitions
- ProviderService (provision, destroy, status, etc.)
- AgentService (register, heartbeat)

**File**: `pkg/provider/proto/provider.proto`

## Usage Examples

### Single-Host (Current, Unchanged)

```yaml
# boxy.yaml
pools:
  - name: ubuntu-containers
    backend: docker  # Local embedded provider
    min_ready: 5
```

```bash
# Start server (all providers local)
boxy serve
```

### Distributed Multi-Host (New)

**Server Configuration**:

```yaml
# boxy.yaml (server)
agents:
  - id: windows-host-01
    address: windows-host.internal:8444
    providers: [hyperv]

pools:
  - name: win-server-vms
    backend: hyperv
    backend_agent: windows-host-01  # Route to agent
    min_ready: 3
```

**Agent Configuration**:

```yaml
# agent.yaml (agent)
agent:
  id: windows-host-01
  server_url: https://boxy-server:8443
  listen_address: 0.0.0.0:8444
  providers: [hyperv]

  tls:
    cert_file: /etc/boxy/agent-cert.pem
    key_file: /etc/boxy/agent-key.pem
    ca_file: /etc/boxy/ca-cert.pem
```

**Commands**:

```bash
# Initialize CA (one-time)
boxy admin init-ca --output /etc/boxy/ca

# Issue agent certificate
boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --agent-id windows-host-01 \
  --output /etc/boxy/agents/windows-01

# Start server
boxy serve --config boxy.yaml

# Start agent (on Windows host)
boxy agent serve --config agent.yaml
```

## Security Model

### Authentication

- **Mutual TLS**: Both server and agent authenticate via certificates
- **Certificate-Based**: No passwords, key-based trust
- **CA-Signed**: All certificates signed by Boxy CA

### Authorization

- **Provider-Level**: Agents authorized per provider
- **Resource Quotas**: Per-agent resource limits
- **Audit Logging**: All operations logged

### Data Protection

- **Encrypted in Transit**: TLS 1.2+ with strong ciphers
- **Encrypted at Rest**: Credentials encrypted in database
- **Short-Lived Certs**: Agent certificates expire in 90 days

## Implementation Phases

### Phase 1: Foundation (Week 1)

- Protocol Buffers & gRPC code generation
- RemoteProvider implementation
- Agent Server implementation

### Phase 2: Security (Week 1)

- Certificate management commands
- mTLS configuration
- Authorization logic

### Phase 3: Agent Mode (Week 2)

- `boxy agent serve` command
- Agent registration & heartbeat
- Health monitoring

### Phase 4: Server Integration (Week 2)

- Configuration schema updates
- Provider routing
- Agent registry

### Phase 5: Observability (Week 3)

- Metrics (Prometheus)
- Tracing (OpenTelemetry)
- Audit logging

### Phase 6: Production Readiness (Week 3)

- Resilience (retries, circuit breakers)
- Stress testing
- Security audit

**Total Timeline**: 3 weeks

## Benefits

### Flexibility

- ✅ Mix Windows and Linux providers
- ✅ Distribute load across hosts
- ✅ Isolate providers for security

### Scalability

- ✅ Add agents as needed
- ✅ Scale providers independently
- ✅ No single point of bottleneck

### Security Checklist

- ✅ mTLS authentication
- ✅ Provider-level authorization
- ✅ Audit all operations
- ✅ Certificate rotation

### Maintainability

- ✅ Single binary (no version skew)
- ✅ Backwards compatible
- ✅ Clear abstractions
- ✅ Comprehensive testing

## Trade-offs

### Complexity Added

- ❌ Certificate management overhead
- ❌ Network communication (latency, failures)
- ❌ More operational complexity
- ❌ Additional monitoring required

### Mitigation Strategies

- ✅ Automate certificate renewal
- ✅ Connection pooling and caching
- ✅ Comprehensive documentation
- ✅ Built-in health checks and metrics

## Testing Strategy

### Unit Tests

- Mock gRPC clients/servers
- Test proto conversions
- Test authorization logic

### Integration Tests

- Real gRPC communication
- Stubbed providers
- Test failure scenarios

### End-to-End Tests

- Full server + agent setup
- Real providers (Docker as analog)
- Test distributed provisioning

### Stress Tests

- High concurrency (100+ requests)
- Multiple agents (10+)
- Large resource counts (1000+)
- Network failure simulation

## Success Criteria

### Performance

- [ ] Provision latency <5s for remote providers
- [ ] Support 1000+ resources across agents
- [ ] Support 10+ agents
- [ ] Handle 100 concurrent requests

### Reliability

- [ ] 99.9% uptime
- [ ] Automatic retry on transient failures
- [ ] Graceful degradation on agent failures

### Security

- [ ] All communication encrypted (mTLS)
- [ ] No credential leaks
- [ ] All operations audited
- [ ] Certificate rotation automated

### Usability

- [ ] Simple configuration
- [ ] Clear error messages
- [ ] Comprehensive documentation
- [ ] Easy troubleshooting

## Migration Path

### Phase 0: Current State (No Changes)

All pools use local providers - continues to work

### Phase 1: Opt-In Remote Providers

Some pools route to agents, others stay local

### Phase 2: Full Distributed

All providers on dedicated agent hosts

**Key Point**: Existing deployments continue working without any changes.

## Documentation

### Architecture

- ✅ [ADR-004: Distributed Agent Architecture](../decisions/adr-004-distributed-agent-architecture.md)
- ✅ [Implementation Guide](distributed-agent-implementation.md)
- ✅ [Security Guide](security-guide.md)

### Implementation

- ✅ [Implementation Roadmap](../IMPLEMENTATION_ROADMAP.md)
- ✅ [Protocol Buffers](../../pkg/provider/proto/provider.proto)

### Operations

- ⏳ Deployment Guide (to be created)
- ⏳ Operations Runbook (to be created)
- ⏳ Troubleshooting Guide (to be created)

## Next Steps

1. **Review & Approve**: Architecture review meeting
2. **Prototype**: Build Phase 1 (Foundation)
3. **Validate**: Test with Docker provider over gRPC
4. **Iterate**: Gather feedback, adjust design
5. **Implement**: Execute full roadmap (3 weeks)
6. **Production**: Deploy to production environment

## Questions & Answers

### Q: Why not just use SSH to run commands on remote hosts?

**A**: SSH lacks type safety, structured error handling, and requires shell scripting. gRPC provides a clean, typed API with built-in auth, retries, and streaming.

### Q: Why single binary instead of separate server/agent binaries?

**A**: Prevents version skew, simplifies deployment, allows running in "both" mode for small deployments, shares code between server and agent.

### Q: What if an agent crashes during provisioning?

**A**: Provisioning is idempotent. On restart, agent reports existing resources. Server can detect incomplete provisions and retry or mark as failed.

### Q: How do we handle network partitions?

**A**: Circuit breakers prevent cascading failures. Agents buffer heartbeats. Server marks agents as degraded after missed heartbeats. Manual intervention may be required for long partitions.

### Q: Can we add providers without recompiling?

**A**: Not in Phase 1. Phase 1 uses compiled-in providers exposed via agents. Future enhancement could support plugin loading on agents.

### Q: How do we load balance across multiple agents with same provider?

**A**: Phase 1: Manual assignment per pool. Phase 2+: Round-robin or capacity-based routing.

## Conclusion

The distributed agent architecture enables Boxy to manage heterogeneous resources across multiple hosts while maintaining security, reliability, and simplicity. The design prioritizes:

1. **Backwards Compatibility**: Existing deployments continue working
2. **Security**: mTLS and strong authentication by default
3. **Simplicity**: Single binary, transparent proxying
4. **Scalability**: Add agents as needed

**Recommendation**: Approve and proceed with Phase 1 implementation.

---

**Document Version**: 1.0
**Last Updated**: 2025-11-20
**Status**: Proposed
**Authors**: Boxy Team
