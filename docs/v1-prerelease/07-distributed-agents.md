# 07: Distributed Agent Architecture

> ARCHIVED: This document was moved to `docs/planning/v1-prerelease/07-distributed-agents.md` for planning and migration.

## History

```yaml
Origin: "docs/v1-prerelease/07-distributed-agents.md"
SourceType: "planning-proposal"
Created: "2024-11-22"
LastMigrated: "2025-11-23"
MigratedBy: "doc-reconciliation"
Status: "archived"
Notes: "Planning copy created in `docs/planning/v1-prerelease/07-distributed-agents.md`. For ADR and final decisions see `docs/decisions/adr-004-distributed-agent-architecture.md`."
```

---

## Metadata

```yaml
feature: "Distributed Agents"
slug: "distributed-agents"
status: "not-started"
priority: "critical"
type: "feature"
effort: "large"
depends_on: ["architecture-refactor"]
enables: ["hyperv-production", "multi-host-providers"]
testing: ["unit", "integration", "e2e", "manual"]
breaking_change: false
week: "5-6"
related_docs:
  - "../decisions/adr-004-distributed-agent-architecture.md"
  - "01-architecture-refactor.md"
```

---

## Overview

**CRITICAL FOR v1**: Hyper-V is the PRIMARY backend (not Docker), and Hyper-V cannot run on Linux. Therefore, distributed agent architecture is ESSENTIAL for v1-prerelease.

### Why This is v1 (Not v2)

**Rationale:**

- Hyper-V is PRIMARY backend for production use
- Hyper-V only runs on Windows hosts
- Boxy server typically runs on Linux for flexibility
- Without agents, Hyper-V cannot be used → v1-prerelease is blocked

**Therefore**: Distributed agents are NOT optional, they are REQUIRED for v1.

### Architecture

```text
┌─────────────────────────────────────────┐
│     Boxy Server (Linux)                  │
│  ┌────────────────────────────────────┐ │
│  │  Provider Registry                 │ │
│  │  ├─ Docker (embedded, local)       │ │
│  │  └─ Hyper-V (remote via agent)     │ │
│  └────────────────┬───────────────────┘ │
└────────────────────┼─────────────────────┘
                     │ gRPC + mTLS
                     ↓
    ┌────────────────────────────────────┐
    │  Boxy Agent (Windows Host)         │
    │  ├─ Hyper-V Provider (embedded)    │
    │  └─ gRPC Server                    │
    └────────────────────────────────────┘
```

**Key Points:**

- **Single binary**: `boxy` runs as server, agent, or both
- **Transparent proxying**: RemoteProvider implements same Provider interface
- **gRPC**: Efficient, type-safe RPC with Protocol Buffers
- **mTLS**: Mutual authentication with client certificates
- **Backwards compatible**: Local providers work unchanged

---

## Component Design

### Binary Modes

```bash
# Run as server (orchestrator)
boxy serve

# Run as agent (provider host)
boxy agent serve \
  --server-url https://boxy-server:8443 \
  --cert /path/to/agent-cert.pem \
  --key /path/to/agent-key.pem \
  --ca /path/to/ca-cert.pem \
  --providers hyperv

# Run as both (server + local agent)
boxy serve --agent-mode
```

### Provider Interface (Unchanged)

```go
// pkg/provider/provider.go
type Provider interface {
    Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error)
    Destroy(ctx context.Context, res *resource.Resource) error
    GetStatus(ctx context.Context, res *resource.Resource) (*resource.ResourceStatus, error)
    GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error)
    Update(ctx context.Context, res *resource.Resource, update ResourceUpdate) error
    Exec(ctx context.Context, res *resource.Resource, command []string) (*ExecResult, error)
    HealthCheck(ctx context.Context) error
    Name() string
    Type() resource.ResourceType
}
```

**Critical**: Remote providers implement the EXACT same interface → transparent to Pool, Sandbox, Allocator.

---

## Implementation Tasks

### Task 7.1: Protocol Buffers

**File**: `pkg/provider/proto/provider.proto`

```protobuf
syntax = "proto3";

package provider;

service ProviderService {
  rpc Provision(ProvisionRequest) returns (ProvisionResponse);
  rpc Destroy(DestroyRequest) returns (DestroyResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc GetConnectionInfo(GetConnectionInfoRequest) returns (GetConnectionInfoResponse);
  rpc Update(UpdateRequest) returns (UpdateResponse);
  rpc Exec(ExecRequest) returns (ExecResponse);
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
  string type = 1;           // "vm", "container", etc.
  string provider_type = 2;  // "hyperv", "docker", etc.
  int32 cpus = 3;
  int64 memory_mb = 4;
  int64 disk_gb = 5;
  string image = 6;
  map<string, string> labels = 7;
  map<string, string> environment = 8;
}

message Resource {
  string id = 1;
  string pool_id = 2;
  string sandbox_id = 3;
  string state = 4;
  string type = 5;
  string provider_type = 6;
  string provider_id = 7;
  map<string, string> metadata = 8;
  int64 created_at = 9;
  int64 updated_at = 10;
}

// ... other messages
```

**Build:**

```bash
# Generate Go code from .proto files
make protobuf
```

---

### Task 7.2: Remote Provider

**File**: `pkg/provider/remote/remote.go`

```go
package remote

type RemoteProvider struct {
    name         string
    resourceType resource.ResourceType
    agentID      string
    client       providerproto.ProviderServiceClient
    conn         *grpc.ClientConn
}

func NewRemoteProvider(
    name string,
    resourceType resource.ResourceType,
    agentID string,
    agentAddress string,
    tlsConfig *tls.Config,
) (*RemoteProvider, error) {
    // Create gRPC connection with mTLS
    creds := credentials.NewTLS(tlsConfig)
    conn, err := grpc.Dial(agentAddress, grpc.WithTransportCredentials(creds))
    if err != nil {
        return nil, err
    }

    client := providerproto.NewProviderServiceClient(conn)

    return &RemoteProvider{
        name:         name,
        resourceType: resourceType,
        agentID:      agentID,
        client:       client,
        conn:         conn,
    }, nil
}

func (r *RemoteProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    // Translate to gRPC request
    req := &providerproto.ProvisionRequest{
        ProviderName: r.name,
        Spec:         specToProto(spec),
    }

    // Call agent via gRPC
    resp, err := r.client.Provision(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("remote provision failed: %w", err)
    }

    // Translate response back
    return protoToResource(resp.Resource), nil
}

// Destroy, GetStatus, GetConnectionInfo, Update, Exec follow same pattern...
```

**Tests:**

```go
// pkg/provider/remote/remote_test.go
func TestRemoteProvider_Provision(t *testing.T)
func TestRemoteProvider_SerializationRoundtrip(t *testing.T)
func TestRemoteProvider_ErrorHandling(t *testing.T)
```

---

### Task 7.3: Agent Server

**File**: `internal/agent/server.go`

```go
package agent

type Server struct {
    registry   *provider.Registry  // Local providers
    tlsConfig  *tls.Config
    grpcServer *grpc.Server
    logger     *logrus.Logger
}

func NewServer(
    registry *provider.Registry,
    tlsConfig *tls.Config,
    logger *logrus.Logger,
) *Server {
    return &Server{
        registry:  registry,
        tlsConfig: tlsConfig,
        logger:    logger,
    }
}

func (s *Server) Start(address string) error {
    creds := credentials.NewTLS(s.tlsConfig)

    s.grpcServer = grpc.NewServer(
        grpc.Creds(creds),
        grpc.UnaryInterceptor(s.authInterceptor),
    )

    providerproto.RegisterProviderServiceServer(s.grpcServer, s)

    lis, err := net.Listen("tcp", address)
    if err != nil {
        return err
    }

    s.logger.WithField("address", address).Info("Agent server starting")
    return s.grpcServer.Serve(lis)
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

func (s *Server) authInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
    // Extract and validate client certificate
    peer, ok := peer.FromContext(ctx)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "no peer info")
    }

    tlsInfo, ok := peer.AuthInfo.(credentials.TLSInfo)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "no TLS info")
    }

    if len(tlsInfo.State.VerifiedChains) == 0 {
        return nil, status.Error(codes.Unauthenticated, "no verified chains")
    }

    // Extract agent ID from certificate CN
    agentID := tlsInfo.State.VerifiedChains[0][0].Subject.CommonName
    s.logger.WithField("agent_id", agentID).Debug("Agent authenticated")

    // Proceed with request
    return handler(ctx, req)
}
```

**Tests:**

```go
// internal/agent/server_test.go
func TestAgentServer_Provision(t *testing.T)
func TestAgentServer_mTLSAuthentication(t *testing.T)
func TestAgentServer_ProviderNotFound(t *testing.T)
```

---

### Task 7.4: Certificate Management

**CLI Commands:**

```bash
# Initialize CA
boxy admin init-ca --output /etc/boxy/ca

# Issue agent certificate
boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --agent-id windows-host-01 \
  --output /etc/boxy/agents/windows-01

# Issue server certificate
boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --server \
  --output /etc/boxy/server
```

**Implementation**: `cmd/boxy/commands/admin_certs.go`

```go
func initCA(outputDir string) error {
    // Generate CA private key
    caKey, err := rsa.GenerateKey(rand.Reader, 4096)

    // Create CA certificate
    template := &x509.Certificate{
        SerialNumber:          big.NewInt(1),
        Subject:               pkix.Name{CommonName: "Boxy CA"},
        NotBefore:             time.Now(),
        NotAfter:              time.Now().AddDate(10, 0, 0),
        KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
        BasicConstraintsValid: true,
        IsCA:                  true,
    }

    caCert, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)

    // Save files
    // - ca-cert.pem
    // - ca-key.pem
}

func issueCert(caCert, caKey, agentID, outputDir string) error {
    // Generate agent private key
    // Create certificate with CN=agentID
    // Sign with CA
    // Save agent-cert.pem and agent-key.pem
}
```

---

### Task 7.5: Configuration

**YAML Schema:**

```yaml
# boxy.yaml
server:
  mode: server
  listen_address: 0.0.0.0:8443
  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca-cert.pem
    client_auth: require

agents:
  - id: windows-host-01
    address: windows-host-01.internal:8444
    providers:
      - hyperv
    tls:
      cert_file: /etc/boxy/agents/windows-01-cert.pem
      key_file: /etc/boxy/agents/windows-01-key.pem
      ca_file: /etc/boxy/ca-cert.pem

pools:
  - name: win-server-2022
    type: vm
    backend: hyperv
    backend_agent: windows-host-01  # Routes to remote agent
    image: win-server-2022-template
    min_ready: 3
    max_total: 10

  - name: ubuntu-containers
    type: container
    backend: docker
    # backend_agent: (not specified = local embedded provider)
    image: ubuntu:22.04
    min_ready: 5
    max_total: 20
```

---

### Task 7.6: Agent CLI Command

**Command**: `boxy agent serve`

**File**: `cmd/boxy/commands/agent.go`

```go
func agentServeCommand() *cobra.Command {
    var (
        serverURL string
        certFile  string
        keyFile   string
        caFile    string
        providers []string
    )

    cmd := &cobra.Command{
        Use:   "serve",
        Short: "Start Boxy agent",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Load TLS config
            tlsConfig, err := loadTLSConfig(certFile, keyFile, caFile)
            if err != nil {
                return err
            }

            // Create provider registry with requested providers
            registry := provider.NewRegistry()
            for _, prov := range providers {
                switch prov {
                case "hyperv":
                    registry.Register(hyperv.NewProvider())
                case "docker":
                    registry.Register(docker.NewProvider())
                // ... other providers
                }
            }

            // Start agent server
            server := agent.NewServer(registry, tlsConfig, logger)
            return server.Start(":8444")
        },
    }

    cmd.Flags().StringVar(&serverURL, "server-url", "", "Boxy server URL")
    cmd.Flags().StringVar(&certFile, "cert", "", "Agent certificate")
    cmd.Flags().StringVar(&keyFile, "key", "", "Agent private key")
    cmd.Flags().StringVar(&caFile, "ca", "", "CA certificate")
    cmd.Flags().StringSliceVar(&providers, "providers", []string{}, "Providers to enable")

    return cmd
}
```

---

## Security Model

### mTLS Authentication Flow

1. Agent starts with client certificate
2. Agent connects to server with mTLS
3. Server verifies agent certificate against CA
4. Server extracts agent ID from cert CN (Common Name)
5. Server validates agent is authorized for requested providers
6. Bidirectional authentication established

### Certificate Lifecycle

- **CA certificate**: Valid for 10 years
- **Agent certificates**: Valid for 1 year, renewable
- **Server certificate**: Valid for 1 year, renewable
- **Rotation**: `boxy admin renew-cert` command

---

## Testing Strategy

### Challenge

Hyper-V only runs on Windows, but CI runs on Linux.

### Solution: Stub Provider

```go
// pkg/provider/stub/hyperv_stub.go
type StubHyperVProvider struct {
    vms map[string]*stubVM
    mu  sync.Mutex
}

func (s *StubHyperVProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    // Simulate realistic behavior
    time.Sleep(10 * time.Second) // Simulate provision time

    s.mu.Lock()
    defer s.mu.Unlock()

    vm := &stubVM{
        ID:    uuid.New().String(),
        Name:  fmt.Sprintf("stub-vm-%s", uuid.New().String()[:8]),
        State: "stopped",
    }
    s.vms[vm.ID] = vm

    return &resource.Resource{
        ID:         uuid.New().String(),
        ProviderID: vm.ID,
        State:      resource.StateProvisioned,
        Metadata: map[string]string{
            "stub": "true",
        },
    }, nil
}

// Destroy, GetStatus, etc. - all stubbed realistically
```

### Testing Layers

1. **Unit tests**: Test RemoteProvider, Agent Server with mocks (Linux OK)
2. **Integration tests**: Test with Docker provider via agent (Linux OK)
3. **E2E tests**: Test with stubbed Hyper-V provider (Linux OK)
4. **Manual tests**: Test with real Hyper-V on Windows host (Windows required)

**Test Files:**

```go
// pkg/provider/remote/remote_test.go - Unit tests
// tests/integration/agent_test.go - Integration with Docker
// tests/e2e/distributed_agent_test.go - E2E with stub
```

---

## Implementation Phases

### Phase 1: Foundation (Week 1)

- [ ] Define Protocol Buffers schema (provider.proto)
- [ ] Generate gRPC code (`make protobuf`)
- [ ] Create RemoteProvider implementation
- [ ] Create Agent Server implementation
- [ ] Unit tests for serialization/deserialization

### Phase 2: Security (Week 1-2)

- [ ] Implement CA initialization (`boxy admin init-ca`)
- [ ] Implement certificate issuance (`boxy admin issue-cert`)
- [ ] mTLS configuration for server
- [ ] mTLS configuration for agent
- [ ] Agent authentication in interceptor
- [ ] Integration tests for mTLS

### Phase 3: Agent Mode (Week 2)

- [ ] Add `boxy agent serve` command
- [ ] Agent heartbeat mechanism
- [ ] Agent health monitoring
- [ ] Graceful shutdown
- [ ] E2E tests with agent

### Phase 4: Server Integration (Week 2)

- [ ] Update configuration schema (`backend_agent` field)
- [ ] Agent registry in server
- [ ] Remote provider factory
- [ ] Provider routing logic (local vs remote)
- [ ] Integration tests

### Phase 5: Testing (Throughout)

- [ ] Unit tests for RemoteProvider
- [ ] Unit tests for Agent Server
- [ ] Integration tests with Docker via agent
- [ ] E2E tests with stub Hyper-V
- [ ] Real Hyper-V testing (manual on Windows)

---

## Success Criteria

- ✅ RemoteProvider implements Provider interface
- ✅ Agent server handles all provider operations
- ✅ mTLS authentication works
- ✅ Certificates can be generated and rotated
- ✅ Integration tests pass with Docker via agent
- ✅ E2E tests pass with stubbed Hyper-V
- ✅ Manual testing with real Hyper-V successful
- ✅ No regressions for local providers
- ✅ Configuration schema updated and documented

---

## User Impact

### Before (Local Only)

```yaml
pools:
  - name: win-vms
    backend: hyperv  # ERROR: Hyper-V can't run on Linux!
```

### After (Distributed)

```yaml
agents:
  - id: windows-host-01
    address: windows-host-01:8444
    providers: [hyperv]

pools:
  - name: win-vms
    backend: hyperv
    backend_agent: windows-host-01  # Routes to Windows agent ✓
```

---

## Related Documents

- [ADR-004: Distributed Agent Architecture](../decisions/adr-004-distributed-agent-architecture.md)
- [01: Architecture Refactor](01-architecture-refactor.md) - Provider interface
- [12: Docker & Compose](12-docker-compose.md) - Deploy agents in containers

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: 01-architecture-refactor (Provider interface must be stable)
**Blocking**: Real Hyper-V testing, production deployment
