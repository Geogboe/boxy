# ADR-004: Distributed Agent Architecture

**Date**: 2025-11-20
**Status**: Proposed

## Context

The current Boxy architecture assumes all providers run embedded in the same process as the server. However, the host running Boxy may not have access to certain provider capabilities:

- Boxy server on Linux host may need to orchestrate Hyper-V VMs on Windows hosts
- Boxy server without Docker may need to provision containers on Docker hosts
- Security isolation: providers run on dedicated hosts with restricted access
- Scalability: distribute resource provisioning across multiple agent hosts

We need a distributed architecture where Boxy server can orchestrate providers running on remote agents.

## Decision

We will implement a **transparent remote provider architecture** using a single binary with dual modes:

### Architecture Principles

1. **Single Binary**: One `boxy` binary can run as server, agent, or both
2. **Transparent Proxying**: Remote providers implement the same `Provider` interface
3. **gRPC Communication**: Agents expose providers via gRPC for efficient, type-safe RPC
4. **Mutual TLS**: Server and agents authenticate via client certificates
5. **Backwards Compatible**: Local (embedded) providers continue to work unchanged

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                     Boxy Server (Linux)                      │
│  ┌────────────────────────────────────────────────────────┐ │
│  │              Provider Registry                         │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────────┐  │ │
│  │  │   Docker     │  │   Hyper-V    │  │     KVM     │  │ │
│  │  │  (embedded)  │  │   (remote)   │  │ (embedded)  │  │ │
│  │  └──────────────┘  └──────┬───────┘  └─────────────┘  │ │
│  └──────────────────────────────┼──────────────────────────┘ │
└──────────────────────────────────┼──────────────────────────┘
                                   │ gRPC + mTLS
                                   ↓
              ┌────────────────────────────────────┐
              │   Boxy Agent (Windows Host)        │
              │  ┌──────────────────────────────┐  │
              │  │   Provider Server            │  │
              │  │   ┌────────────────────┐     │  │
              │  │   │  Hyper-V Provider  │     │  │
              │  │   │    (embedded)      │     │  │
              │  │   └────────────────────┘     │  │
              │  └──────────────────────────────┘  │
              └────────────────────────────────────┘
```

### Component Design

#### 1. Binary Modes

```bash
# Run as server (orchestrator)
boxy serve

# Run as agent (provider host)
boxy agent serve \
  --server-url https://boxy-server:8443 \
  --cert /path/to/agent-cert.pem \
  --key /path/to/agent-key.pem \
  --ca /path/to/ca-cert.pem \
  --providers docker,hyperv

# Run as both (server + local agent)
boxy serve --agent-mode
```

#### 2. Provider Interface (Unchanged)

```go
// pkg/provider/provider.go
type Provider interface {
    Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error)
    Destroy(ctx context.Context, res *resource.Resource) error
    GetStatus(ctx context.Context, res *resource.Resource) (*resource.ResourceStatus, error)
    GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error)
    HealthCheck(ctx context.Context) error
    Name() string
    Type() resource.ResourceType
}
```

**Key Point**: Remote providers implement the exact same interface - transparent to consumers.

#### 3. Remote Provider (New)

```go
// pkg/provider/remote/remote.go
type RemoteProvider struct {
    name         string
    resourceType resource.ResourceType
    agentID      string
    client       providerproto.ProviderServiceClient
    conn         *grpc.ClientConn
}

func (r *RemoteProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    // Translate to gRPC request
    req := &providerproto.ProvisionRequest{
        Spec: specToProto(spec),
    }

    // Call agent via gRPC
    resp, err := r.client.Provision(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("remote provision failed: %w", err)
    }

    // Translate response back
    return protoToResource(resp.Resource), nil
}

// Other methods follow same pattern...
```

#### 4. Agent Server (New)

```go
// internal/agent/server.go
type Server struct {
    registry   *provider.Registry  // Local providers
    tlsConfig  *tls.Config
    grpcServer *grpc.Server
}

func (s *Server) Provision(ctx context.Context, req *providerproto.ProvisionRequest) (*providerproto.ProvisionResponse, error) {
    // Get local provider
    prov, ok := s.registry.Get(req.ProviderName)
    if !ok {
        return nil, status.Errorf(codes.NotFound, "provider not found: %s", req.ProviderName)
    }

    // Call local provider
    spec := protoToSpec(req.Spec)
    res, err := prov.Provision(ctx, spec)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "provision failed: %v", err)
    }

    return &providerproto.ProvisionResponse{
        Resource: resourceToProto(res),
    }, nil
}
```

#### 5. Agent Registration & Discovery

```go
// Server maintains agent registry
type AgentRegistry struct {
    mu     sync.RWMutex
    agents map[string]*AgentInfo
}

type AgentInfo struct {
    ID           string
    Address      string
    Providers    []string  // ["docker", "hyperv"]
    LastHeartbeat time.Time
    Status       AgentStatus
}

// Agents register on startup and send heartbeats
func (s *Server) RegisterAgent(ctx context.Context, req *RegisterRequest) (*RegisterResponse, error) {
    // Verify mTLS cert
    peer, ok := peer.FromContext(ctx)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "no peer info")
    }

    // Extract client cert
    tlsInfo := peer.AuthInfo.(credentials.TLSInfo)
    cert := tlsInfo.State.PeerCertificates[0]

    // Verify cert is signed by our CA and get agent ID from cert CN
    agentID := cert.Subject.CommonName

    // Register agent
    s.agentRegistry.Register(agentID, req.Address, req.Providers)

    return &RegisterResponse{Success: true}, nil
}
```

#### 6. Configuration

```yaml
# ~/.config/boxy/boxy.yaml

# Server configuration
server:
  mode: server
  listen_address: 0.0.0.0:8443
  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca-cert.pem
    client_auth: require

# Agent configuration
agents:
  - id: windows-host-01
    address: windows-host-01.internal:8444
    providers:
      - hyperv
    tls:
      cert_file: /etc/boxy/agents/windows-01-cert.pem
      key_file: /etc/boxy/agents/windows-01-key.pem
      ca_file: /etc/boxy/ca-cert.pem

# Pool configuration (unchanged)
pools:
  - name: win-server-2022
    type: vm
    backend: hyperv           # Transparently routed to agent
    backend_agent: windows-host-01  # NEW: specify which agent
    image: win-server-2022-template
    min_ready: 3
    max_total: 10

  - name: ubuntu-containers
    type: container
    backend: docker           # Local provider
    # backend_agent: (not specified = local)
    image: ubuntu:22.04
    min_ready: 5
    max_total: 20
```

### Security Model

#### Certificate Management

```bash
# Server generates CA and issues certificates
boxy admin init-ca \
  --output /etc/boxy/ca

# Generate agent certificate
boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --agent-id windows-host-01 \
  --output /etc/boxy/agents/windows-01

# Outputs:
#   windows-01-cert.pem
#   windows-01-key.pem
```

#### mTLS Authentication Flow

1. Agent starts with client certificate
2. Agent connects to server with mTLS
3. Server verifies agent certificate against CA
4. Server extracts agent ID from cert CN (Common Name)
5. Server validates agent is authorized for requested providers
6. Bidirectional authentication established

#### Authorization

```go
// Server validates agent is authorized for provider
func (s *Server) authorizeAgent(agentID, providerName string) error {
    agent, ok := s.agentRegistry.Get(agentID)
    if !ok {
        return fmt.Errorf("agent not registered: %s", agentID)
    }

    if !contains(agent.Providers, providerName) {
        return fmt.Errorf("agent %s not authorized for provider %s", agentID, providerName)
    }

    return nil
}
```

### Protocol Buffers Definition

```protobuf
// pkg/provider/proto/provider.proto
syntax = "proto3";

package provider;

service ProviderService {
  rpc Provision(ProvisionRequest) returns (ProvisionResponse);
  rpc Destroy(DestroyRequest) returns (DestroyResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc GetConnectionInfo(GetConnectionInfoRequest) returns (GetConnectionInfoResponse);
  rpc HealthCheck(HealthCheckRequest) returns (HealthCheckResponse);
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
}

message ProvisionRequest {
  string provider_name = 1;
  ResourceSpec spec = 2;
}

message ProvisionResponse {
  Resource resource = 1;
}

message ResourceSpec {
  string type = 1;
  string provider_type = 2;
  string image = 3;
  int32 cpus = 4;
  int32 memory_mb = 5;
  int32 disk_gb = 6;
  map<string, string> labels = 7;
  map<string, string> environment = 8;
}

message Resource {
  string id = 1;
  string pool_id = 2;
  string type = 3;
  string state = 4;
  string provider_type = 5;
  string provider_id = 6;
  map<string, string> metadata = 7;
  int64 created_at = 8;
  int64 updated_at = 9;
}

// ... other messages
```

## Rationale

### Why gRPC?

1. **Type Safety**: Protocol Buffers provide strong typing
2. **Performance**: Binary protocol, HTTP/2, multiplexing
3. **Streaming**: Support for bidirectional streaming (future: logs, metrics)
4. **Code Generation**: Auto-generate client/server code
5. **Cross-Language**: Future support for agents in other languages
6. **Industry Standard**: Used by Kubernetes, etcd, Consul

### Why Transparent Proxying?

1. **Zero Changes to Core**: Pool managers work unchanged
2. **Simple Testing**: Mock remote providers like any provider
3. **Gradual Migration**: Mix local and remote providers
4. **Clear Abstraction**: Network details hidden from business logic

### Why Single Binary?

1. **Simplified Distribution**: One artifact for all deployments
2. **Code Reuse**: Providers used by both server and agent
3. **Versioning**: Server and agent always compatible
4. **Testing**: Integration tests use same binary

### Why mTLS?

1. **Mutual Authentication**: Both parties verify identity
2. **No Passwords**: Certificate-based trust
3. **Encryption**: TLS protects data in transit
4. **Revocation**: Revoke compromised agent certs via CRL/OCSP
5. **Standard Practice**: Used by Kubernetes, Consul, etcd

## Implementation Plan

### Phase 1: Foundation (Week 1)

- [ ] Define Protocol Buffers schema
- [ ] Generate gRPC code
- [ ] Create RemoteProvider implementation
- [ ] Create Agent Server implementation
- [ ] Unit tests for serialization/deserialization

### Phase 2: Security (Week 1)

- [ ] Implement CA initialization (`boxy admin init-ca`)
- [ ] Implement certificate issuance (`boxy admin issue-cert`)
- [ ] mTLS configuration for server
- [ ] mTLS configuration for agent
- [ ] Agent registration and authorization
- [ ] Integration tests for mTLS

### Phase 3: Agent Mode (Week 2)

- [ ] Add `boxy agent serve` command
- [ ] Agent registration on startup
- [ ] Heartbeat mechanism
- [ ] Agent health monitoring
- [ ] Graceful shutdown
- [ ] E2E tests with real agents

### Phase 4: Server Integration (Week 2)

- [ ] Update configuration schema (backend_agent field)
- [ ] Agent registry in server
- [ ] Remote provider factory
- [ ] Provider routing logic
- [ ] Agent failover handling
- [ ] Integration tests

### Phase 5: Observability (Week 3)

- [ ] Metrics (latency, throughput, errors)
- [ ] Distributed tracing
- [ ] Logging with request IDs
- [ ] Agent status dashboard
- [ ] Monitoring best practices doc

### Phase 6: Production Readiness (Week 3)

- [ ] Connection pooling
- [ ] Retry logic with backoff
- [ ] Circuit breakers
- [ ] Timeouts and deadlines
- [ ] Rate limiting
- [ ] Stress tests
- [ ] Security audit
- [ ] Deployment guide

## Testing Strategy

### Unit Tests

```go
// Test remote provider translates calls correctly
func TestRemoteProvider_Provision(t *testing.T) {
    // Mock gRPC client
    mockClient := &mockProviderClient{}

    provider := &RemoteProvider{
        name: "docker",
        client: mockClient,
    }

    spec := resource.ResourceSpec{
        Type: resource.ResourceTypeContainer,
        Image: "ubuntu:22.04",
    }

    res, err := provider.Provision(context.Background(), spec)
    assert.NoError(t, err)
    assert.NotNil(t, res)

    // Verify gRPC call was made correctly
    assert.Equal(t, 1, mockClient.ProvisionCallCount())
}
```

### Integration Tests

```go
// Test agent server with real Docker provider
func TestAgentServer_WithDockerProvider(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Start agent server with Docker provider
    agent := startTestAgent(t, "docker")
    defer agent.Stop()

    // Create remote provider client
    remote := newRemoteProvider(t, agent.Address(), "docker")

    // Provision via remote provider
    spec := resource.ResourceSpec{
        Type: resource.ResourceTypeContainer,
        Image: "alpine:latest",
    }

    res, err := remote.Provision(context.Background(), spec)
    assert.NoError(t, err)
    assert.NotEmpty(t, res.ProviderID)

    // Verify container exists on agent host
    verifyDockerContainer(t, res.ProviderID)

    // Cleanup
    err = remote.Destroy(context.Background(), res)
    assert.NoError(t, err)
}
```

### End-to-End Tests

```go
// Test full server-agent flow
func TestE2E_ServerAgentProvisioning(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping e2e test")
    }

    // Setup CA
    ca := setupTestCA(t)

    // Start agent with certificate
    agentCert := ca.IssueCert("test-agent", []string{"docker"})
    agent := startAgent(t, agentCert)
    defer agent.Stop()

    // Start server
    server := startServer(t, ca, withAgent("test-agent", agent.Address()))
    defer server.Stop()

    // Create pool using remote provider
    pool := createPool(t, server, PoolConfig{
        Name: "test-pool",
        Backend: "docker",
        BackendAgent: "test-agent",
        MinReady: 1,
    })

    // Wait for pool to provision
    waitForPoolReady(t, pool, 1)

    // Verify resource was provisioned on agent
    stats := pool.GetStats()
    assert.Equal(t, 1, stats.TotalReady)
}
```

### Stub/Mock Strategy

```go
// When real agents unavailable, use in-memory stub
type StubAgentServer struct {
    provider provider.Provider // Mock provider
}

func (s *StubAgentServer) Provision(ctx context.Context, req *proto.ProvisionRequest) (*proto.ProvisionResponse, error) {
    // Use mock provider locally
    spec := protoToSpec(req.Spec)
    res, err := s.provider.Provision(ctx, spec)
    if err != nil {
        return nil, err
    }
    return &proto.ProvisionResponse{Resource: resourceToProto(res)}, nil
}

// Tests can use stub instead of real agent
func TestWithStubAgent(t *testing.T) {
    stub := &StubAgentServer{
        provider: mock.NewProvider(), // Mock provider
    }

    // Test as if talking to real agent
    // ...
}
```

## Consequences

### Positive

- ✅ Supports distributed deployments
- ✅ Provider location transparent to core logic
- ✅ Secure authentication and encryption
- ✅ Backwards compatible (local providers work unchanged)
- ✅ Scalable (add agents as needed)
- ✅ Type-safe RPC with Protocol Buffers
- ✅ Single binary simplifies deployment

### Negative

- ❌ Increased complexity (network calls, serialization, mTLS)
- ❌ New failure modes (network failures, agent crashes)
- ❌ Latency overhead for remote calls
- ❌ Certificate management overhead
- ❌ More operational complexity (multiple hosts)

### Mitigation Strategies

| Risk | Mitigation |
|------|------------|
| Network failures | Retry with exponential backoff, circuit breakers |
| Agent crashes | Health checks, automatic failover to other agents |
| Latency | Connection pooling, keep-alive, local caching |
| Cert management | Automated cert renewal, clear rotation procedures |
| Debugging | Distributed tracing, correlation IDs, detailed logs |

## Migration Path

### Phase 0: Current (All Local)

```yaml
pools:
  - name: ubuntu-containers
    backend: docker  # Local embedded provider
```

### Phase 1: Mixed (Local + Remote)

```yaml
pools:
  - name: ubuntu-containers
    backend: docker  # Still local

  - name: win-server-vms
    backend: hyperv
    backend_agent: windows-host-01  # Remote
```

### Phase 2: Full Remote

```yaml
pools:
  - name: ubuntu-containers
    backend: docker
    backend_agent: docker-host-01  # All remote

  - name: win-server-vms
    backend: hyperv
    backend_agent: windows-host-01
```

## Alternatives Considered

### 1. REST API Instead of gRPC

**Rejected**:
- Less efficient (JSON, HTTP/1.1)
- No streaming support
- Manual client code
- Less type safety

### 2. Separate Binaries (boxy-server, boxy-agent)

**Rejected**:
- Version skew between server and agent
- More complex distribution
- Duplicate code/dependencies
- Harder to test

### 3. Embedded Agents (libvirt-style)

**Rejected**:
- Can't run on different OS (Linux server managing Windows)
- Doesn't solve security isolation
- Limited scalability

### 4. SSH-Based Remote Execution

**Rejected**:
- Less structured than gRPC
- Harder to manage state
- No type safety
- Complex error handling

## Security Considerations

### Threat Model

| Threat | Mitigation |
|--------|------------|
| Man-in-the-Middle | mTLS with certificate pinning |
| Rogue Agent | Certificate-based authentication, revocation |
| Credential Theft | Encrypted credentials, short-lived certs |
| DoS Attack | Rate limiting, connection limits |
| Privilege Escalation | Agent authorization per provider |
| Data Exfiltration | Audit logging, network segmentation |

### Best Practices

1. **Principle of Least Privilege**: Agents only authorized for specific providers
2. **Defense in Depth**: mTLS + authorization + audit logging
3. **Secure Defaults**: Require client auth, strong cipher suites
4. **Audit Trail**: Log all agent operations with request IDs
5. **Regular Rotation**: Automate certificate renewal
6. **Monitoring**: Alert on auth failures, unusual patterns

## Open Questions

1. **Agent Discovery**: Should agents register with server, or server configured with static agents?
   - **Decision**: Hybrid - static config + dynamic registration for validation

2. **Failover**: If agent crashes, should server failover to another agent?
   - **Decision**: Phase 2 feature - manual failover initially, auto-failover later

3. **Load Balancing**: Multiple agents with same provider - how to distribute?
   - **Decision**: Phase 3 feature - round-robin, then agent capacity-aware

4. **Streaming**: Should we stream logs/metrics from agents?
   - **Decision**: Phase 4 feature - polling initially, streaming later

## References

- [ADR-002: Provider Architecture](adr-002-provider-architecture.md)
- [gRPC Security Best Practices](https://grpc.io/docs/guides/auth/)
- [mTLS in Kubernetes](https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster/)
- [HashiCorp Consul Agent Communication](https://www.consul.io/docs/security/encryption)
