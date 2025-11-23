# Distributed Agent Implementation Guide

This document provides a detailed implementation guide for Boxy's distributed agent architecture.

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Component Details](#component-details)
3. [Implementation Phases](#implementation-phases)
4. [Testing Strategy](#testing-strategy)
5. [Security Implementation](#security-implementation)
6. [Deployment Guide](#deployment-guide)
7. [Troubleshooting](#troubleshooting)

## Architecture Overview

### High-Level Flow

```text
┌──────────────┐                  ┌──────────────┐
│              │                  │              │
│  Pool        │  1. Allocate     │  Provider    │
│  Manager     │──────────────────▶  Registry    │
│              │                  │              │
└──────────────┘                  └──────┬───────┘
                                         │
                        ┌────────────────┴────────────────┐
                        │                                 │
                  2. Local?                          Remote?
                        │                                 │
                  ┌─────▼──────┐                  ┌──────▼─────┐
                  │            │                  │            │
                  │  Docker    │                  │  Remote    │
                  │  Provider  │                  │  Provider  │
                  │ (embedded) │                  │  (proxy)   │
                  │            │                  │            │
                  └────────────┘                  └──────┬─────┘
                                                         │
                                                   3. gRPC call
                                                         │
                                                  ┌──────▼─────┐
                                                  │            │
                                                  │   Agent    │
                                                  │   Server   │
                                                  │            │
                                                  └──────┬─────┘
                                                         │
                                                   4. Local call
                                                         │
                                                  ┌──────▼─────┐
                                                  │            │
                                                  │  Hyper-V   │
                                                  │  Provider  │
                                                  │ (embedded) │
                                                  │            │
                                                  └────────────┘
```

### Key Design Decisions

1. **Transparency**: Remote providers implement the same interface as local providers
2. **Single Binary**: One `boxy` executable runs as server, agent, or both
3. **Type Safety**: gRPC with Protocol Buffers for all remote communication
4. **Security**: Mutual TLS (mTLS) with certificate-based authentication
5. **Backwards Compatible**: Existing local providers work unchanged

## Component Details

### 1. Remote Provider (pkg/provider/remote)

**Purpose**: Client-side proxy that implements Provider interface and translates calls to gRPC.

**File**: `pkg/provider/remote/remote.go`

```go
package remote

import (
    "context"
    "fmt"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"

    "github.com/Geogboe/boxy/internal/core/resource"
    "github.com/Geogboe/boxy/pkg/provider"
    pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

type RemoteProvider struct {
    name         string
    resourceType resource.ResourceType
    agentID      string
    agentAddress string
    conn         *grpc.ClientConn
    client       pb.ProviderServiceClient
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
    conn, err := grpc.Dial(
        agentAddress,
        grpc.WithTransportCredentials(creds),
        grpc.WithBlock(),
        grpc.WithTimeout(10*time.Second),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to agent: %w", err)
    }

    client := pb.NewProviderServiceClient(conn)

    return &RemoteProvider{
        name:         name,
        resourceType: resourceType,
        agentID:      agentID,
        agentAddress: agentAddress,
        conn:         conn,
        client:       client,
    }, nil
}

func (r *RemoteProvider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
    req := &pb.ProvisionRequest{
        ProviderName: r.name,
        Spec:         specToProto(spec),
    }

    resp, err := r.client.Provision(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("remote provision failed: %w", err)
    }

    return protoToResource(resp.Resource), nil
}

// Other methods follow same pattern...

func (r *RemoteProvider) Close() error {
    return r.conn.Close()
}

// Ensure implements provider.Provider interface
var _ provider.Provider = (*RemoteProvider)(nil)
```

**Key Features**:

- Maintains persistent gRPC connection to agent
- Translates domain objects to/from Protocol Buffers
- Handles connection errors and retries
- Implements same interface as local providers

### 2. Agent Server (internal/agent)

**Purpose**: Server-side component that exposes local providers via gRPC.

**File**: `internal/agent/server.go`

```go
package agent

import (
    "context"
    "fmt"
    "net"
    "crypto/tls"

    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/credentials"
    "google.golang.org/grpc/peer"
    "google.golang.org/grpc/status"
    "github.com/sirupsen/logrus"

    "github.com/Geogboe/boxy/pkg/provider"
    pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

type Server struct {
    pb.UnimplementedProviderServiceServer
    pb.UnimplementedAgentServiceServer

    agentID       string
    listenAddr    string
    registry      *provider.Registry
    tlsConfig     *tls.Config
    grpcServer    *grpc.Server
    logger        *logrus.Logger
}

func NewServer(
    agentID string,
    listenAddr string,
    registry *provider.Registry,
    tlsConfig *tls.Config,
    logger *logrus.Logger,
) *Server {
    return &Server{
        agentID:    agentID,
        listenAddr: listenAddr,
        registry:   registry,
        tlsConfig:  tlsConfig,
        logger:     logger,
    }
}

func (s *Server) Start() error {
    listener, err := net.Listen("tcp", s.listenAddr)
    if err != nil {
        return fmt.Errorf("failed to listen: %w", err)
    }

    // Create gRPC server with mTLS
    creds := credentials.NewTLS(s.tlsConfig)
    s.grpcServer = grpc.NewServer(
        grpc.Creds(creds),
        grpc.UnaryInterceptor(s.loggingInterceptor),
    )

    // Register services
    pb.RegisterProviderServiceServer(s.grpcServer, s)
    pb.RegisterAgentServiceServer(s.grpcServer, s)

    s.logger.WithField("address", s.listenAddr).Info("Agent server starting")

    return s.grpcServer.Serve(listener)
}

func (s *Server) Stop() {
    if s.grpcServer != nil {
        s.grpcServer.GracefulStop()
    }
}

// ===== ProviderService Implementation =====

func (s *Server) Provision(ctx context.Context, req *pb.ProvisionRequest) (*pb.ProvisionResponse, error) {
    // Get provider
    prov, ok := s.registry.Get(req.ProviderName)
    if !ok {
        return nil, status.Errorf(codes.NotFound, "provider not found: %s", req.ProviderName)
    }

    // Authorize request
    if err := s.authorizeRequest(ctx, req.ProviderName); err != nil {
        return nil, status.Errorf(codes.PermissionDenied, "unauthorized: %v", err)
    }

    // Translate proto to domain object
    spec := protoToSpec(req.Spec)

    // Call local provider
    res, err := prov.Provision(ctx, spec)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "provision failed: %v", err)
    }

    // Translate domain object to proto
    return &pb.ProvisionResponse{
        Resource: resourceToProto(res),
    }, nil
}

// Other ProviderService methods...

// ===== AgentService Implementation =====

func (s *Server) ListProviders(ctx context.Context, req *pb.ListProvidersRequest) (*pb.ListProvidersResponse, error) {
    providerNames := s.registry.List()
    providers := make([]*pb.ProviderInfo, 0, len(providerNames))

    for _, name := range providerNames {
        prov, _ := s.registry.Get(name)

        // Health check
        healthy := true
        if err := prov.HealthCheck(ctx); err != nil {
            healthy = false
        }

        providers = append(providers, &pb.ProviderInfo{
            Name:    prov.Name(),
            Type:    string(prov.Type()),
            Healthy: healthy,
        })
    }

    return &pb.ListProvidersResponse{
        Providers: providers,
    }, nil
}

// ===== Helper Methods =====

func (s *Server) authorizeRequest(ctx context.Context, providerName string) error {
    // Extract client certificate from context
    peer, ok := peer.FromContext(ctx)
    if !ok {
        return fmt.Errorf("no peer info in context")
    }

    tlsInfo, ok := peer.AuthInfo.(credentials.TLSInfo)
    if !ok {
        return fmt.Errorf("no TLS info in peer")
    }

    if len(tlsInfo.State.PeerCertificates) == 0 {
        return fmt.Errorf("no client certificate")
    }

    cert := tlsInfo.State.PeerCertificates[0]
    clientID := cert.Subject.CommonName

    // Verify client is authorized (in real impl, check against allowlist)
    s.logger.WithFields(logrus.Fields{
        "client_id": clientID,
        "provider":  providerName,
    }).Debug("Authorized request")

    return nil
}

func (s *Server) loggingInterceptor(
    ctx context.Context,
    req interface{},
    info *grpc.UnaryServerInfo,
    handler grpc.UnaryHandler,
) (interface{}, error) {
    s.logger.WithField("method", info.FullMethod).Debug("gRPC call")
    return handler(ctx, req)
}
```

### 3. Agent Registration (internal/agent/registration.go)

**Purpose**: Agent registers with server on startup and sends heartbeats.

```go
package agent

import (
    "context"
    "fmt"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"

    pb "github.com/Geogboe/boxy/pkg/provider/proto"
)

type RegistrationClient struct {
    serverAddr string
    agentID    string
    tlsConfig  *tls.Config
    client     pb.AgentServiceClient
    conn       *grpc.ClientConn
}

func NewRegistrationClient(serverAddr, agentID string, tlsConfig *tls.Config) (*RegistrationClient, error) {
    creds := credentials.NewTLS(tlsConfig)
    conn, err := grpc.Dial(
        serverAddr,
        grpc.WithTransportCredentials(creds),
        grpc.WithBlock(),
        grpc.WithTimeout(10*time.Second),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to server: %w", err)
    }

    client := pb.NewAgentServiceClient(conn)

    return &RegistrationClient{
        serverAddr: serverAddr,
        agentID:    agentID,
        tlsConfig:  tlsConfig,
        client:     client,
        conn:       conn,
    }, nil
}

func (r *RegistrationClient) Register(ctx context.Context, listenAddr string, providers []string) error {
    req := &pb.RegisterRequest{
        AgentId:   r.agentID,
        Address:   listenAddr,
        Providers: providers,
        Version:   "0.1.0", // TODO: actual version
        Hostname:  getHostname(),
        Os:        runtime.GOOS,
        Arch:      runtime.GOARCH,
    }

    resp, err := r.client.Register(ctx, req)
    if err != nil {
        return fmt.Errorf("registration failed: %w", err)
    }

    if !resp.Success {
        return fmt.Errorf("registration rejected: %s", resp.Message)
    }

    return nil
}

func (r *RegistrationClient) StartHeartbeat(ctx context.Context, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if err := r.sendHeartbeat(ctx); err != nil {
                // Log error but continue
                fmt.Printf("Heartbeat failed: %v\n", err)
            }
        }
    }
}

func (r *RegistrationClient) sendHeartbeat(ctx context.Context) error {
    req := &pb.HeartbeatRequest{
        AgentId:       r.agentID,
        UptimeSeconds: int64(getUptime().Seconds()),
    }

    resp, err := r.client.Heartbeat(ctx, req)
    if err != nil {
        return err
    }

    if !resp.Success {
        return fmt.Errorf("heartbeat rejected")
    }

    return nil
}

func (r *RegistrationClient) Close() error {
    return r.conn.Close()
}
```

### 4. Certificate Management (internal/cert)

**Purpose**: Generate CA, issue certificates, manage TLS config.

```go
package cert

import (
    "crypto/rand"
    "crypto/rsa"
    "crypto/tls"
    "crypto/x509"
    "crypto/x509/pkix"
    "encoding/pem"
    "fmt"
    "math/big"
    "os"
    "time"
)

type CA struct {
    cert *x509.Certificate
    key  *rsa.PrivateKey
}

// InitCA creates a new Certificate Authority
func InitCA(outputDir string) (*CA, error) {
    // Generate CA private key
    key, err := rsa.GenerateKey(rand.Reader, 4096)
    if err != nil {
        return nil, fmt.Errorf("failed to generate key: %w", err)
    }

    // Create CA certificate
    template := &x509.Certificate{
        SerialNumber: big.NewInt(1),
        Subject: pkix.Name{
            Organization: []string{"Boxy"},
            CommonName:   "Boxy Root CA",
        },
        NotBefore:             time.Now(),
        NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years
        KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
        BasicConstraintsValid: true,
        IsCA:                  true,
    }

    certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
    if err != nil {
        return nil, fmt.Errorf("failed to create certificate: %w", err)
    }

    cert, err := x509.ParseCertificate(certBytes)
    if err != nil {
        return nil, fmt.Errorf("failed to parse certificate: %w", err)
    }

    // Save to disk
    if err := saveCert(outputDir+"/ca-cert.pem", certBytes); err != nil {
        return nil, err
    }
    if err := saveKey(outputDir+"/ca-key.pem", key); err != nil {
        return nil, err
    }

    return &CA{cert: cert, key: key}, nil
}

// IssueCert issues a certificate signed by the CA
func (ca *CA) IssueCert(agentID string, outputDir string) error {
    // Generate key for agent
    key, err := rsa.GenerateKey(rand.Reader, 2048)
    if err != nil {
        return fmt.Errorf("failed to generate key: %w", err)
    }

    // Create certificate template
    serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
    template := &x509.Certificate{
        SerialNumber: serialNumber,
        Subject: pkix.Name{
            Organization: []string{"Boxy"},
            CommonName:   agentID,
        },
        NotBefore:   time.Now(),
        NotAfter:    time.Now().AddDate(1, 0, 0), // 1 year
        KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
        ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
    }

    // Sign certificate with CA
    certBytes, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
    if err != nil {
        return fmt.Errorf("failed to create certificate: %w", err)
    }

    // Save to disk
    certFile := fmt.Sprintf("%s/%s-cert.pem", outputDir, agentID)
    keyFile := fmt.Sprintf("%s/%s-key.pem", outputDir, agentID)

    if err := saveCert(certFile, certBytes); err != nil {
        return err
    }
    if err := saveKey(keyFile, key); err != nil {
        return err
    }

    return nil
}

// LoadCA loads an existing CA
func LoadCA(certPath, keyPath string) (*CA, error) {
    certPEM, err := os.ReadFile(certPath)
    if err != nil {
        return nil, err
    }

    keyPEM, err := os.ReadFile(keyPath)
    if err != nil {
        return nil, err
    }

    certBlock, _ := pem.Decode(certPEM)
    cert, err := x509.ParseCertificate(certBlock.Bytes)
    if err != nil {
        return nil, err
    }

    keyBlock, _ := pem.Decode(keyPEM)
    key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
    if err != nil {
        return nil, err
    }

    return &CA{cert: cert, key: key}, nil
}

// LoadTLSConfig loads TLS configuration from cert/key/ca files
func LoadTLSConfig(certFile, keyFile, caFile string, clientAuth bool) (*tls.Config, error) {
    // Load certificate and key
    cert, err := tls.LoadX509KeyPair(certFile, keyFile)
    if err != nil {
        return nil, fmt.Errorf("failed to load key pair: %w", err)
    }

    // Load CA certificate
    caPEM, err := os.ReadFile(caFile)
    if err != nil {
        return nil, fmt.Errorf("failed to read CA: %w", err)
    }

    caPool := x509.NewCertPool()
    if !caPool.AppendCertsFromPEM(caPEM) {
        return nil, fmt.Errorf("failed to parse CA certificate")
    }

    config := &tls.Config{
        Certificates: []tls.Certificate{cert},
        RootCAs:      caPool,
        ClientCAs:    caPool,
        MinVersion:   tls.VersionTLS12,
    }

    if clientAuth {
        config.ClientAuth = tls.RequireAndVerifyClientCert
    }

    return config, nil
}

// Helper functions
func saveCert(filename string, certBytes []byte) error {
    certPEM := pem.EncodeToMemory(&pem.Block{
        Type:  "CERTIFICATE",
        Bytes: certBytes,
    })
    return os.WriteFile(filename, certPEM, 0644)
}

func saveKey(filename string, key *rsa.PrivateKey) error {
    keyPEM := pem.EncodeToMemory(&pem.Block{
        Type:  "RSA PRIVATE KEY",
        Bytes: x509.MarshalPKCS1PrivateKey(key),
    })
    return os.WriteFile(filename, keyPEM, 0600)
}
```

## Implementation Phases

See [ADR-004](../decisions/adr-004-distributed-agent-architecture.md) for detailed phase breakdown.

### Quick Reference

**Phase 1: Foundation** (Week 1)

- Protocol Buffers & gRPC code generation
- RemoteProvider implementation
- Agent Server implementation

**Phase 2: Security** (Week 1)

- CA initialization command
- Certificate issuance command
- mTLS configuration

**Phase 3: Agent Mode** (Week 2)

- `boxy agent serve` command
- Registration & heartbeat
- Agent health monitoring

**Phase 4: Server Integration** (Week 2)

- Configuration updates
- Agent registry
- Provider routing

**Phase 5: Observability** (Week 3)

- Metrics, tracing, logging

**Phase 6: Production Readiness** (Week 3)

- Resilience features
- Stress testing
- Security audit

## Testing Strategy

### Unit Tests

Test each component in isolation with mocks.

```bash
# Test remote provider
go test ./pkg/provider/remote -v

# Test agent server
go test ./internal/agent -v

# Test certificate management
go test ./internal/cert -v
```

### Integration Tests

Test components working together with real gRPC but stubbed providers.

```bash
# Test agent server with mock provider
go test ./tests/integration/agent_test.go -v

# Test remote provider with stub agent
go test ./tests/integration/remote_provider_test.go -v
```

### End-to-End Tests

Test full flow with real providers (Docker as analog for testing).

```bash
# Test server-agent communication with Docker
go test ./tests/e2e/distributed_test.go -v

# Skip E2E tests in short mode
go test ./... -short
```

### Test Pyramid

```text
           ╱╲
          ╱  ╲  E2E Tests (few, slow, real providers)
         ╱────╲
        ╱      ╲  Integration Tests (moderate, stubbed)
       ╱────────╲
      ╱          ╲  Unit Tests (many, fast, mocked)
     ╱────────────╲
```

## Security Implementation

### Certificate Lifecycle

1. **Initialization**:

   ```bash
   boxy admin init-ca --output /etc/boxy/ca
   ```

2. **Issue Agent Cert**:

   ```bash
   boxy admin issue-cert \
     --ca-cert /etc/boxy/ca/ca-cert.pem \
     --ca-key /etc/boxy/ca/ca-key.pem \
     --agent-id windows-host-01 \
     --output /etc/boxy/agents/windows-01
   ```

3. **Distribution**: Securely copy certs to agent host

4. **Renewal**: Before expiration (cron job or manual)

### Best Practices

- [ ] Store CA private key in hardware security module (HSM) for production
- [ ] Use short-lived certificates (30-90 days)
- [ ] Implement certificate revocation (CRL or OCSP)
- [ ] Rotate certificates before expiration
- [ ] Monitor certificate expiration (alert 30 days before)
- [ ] Audit all certificate operations
- [ ] Use strong cipher suites only
- [ ] Enable perfect forward secrecy

## Deployment Guide

### Single-Host Development

```bash
# Start server with local providers
boxy serve
```

### Multi-Host Production

**Server Host (Linux)**:

```bash
# Start server
boxy serve \
  --config /etc/boxy/boxy.yaml \
  --tls-cert /etc/boxy/server-cert.pem \
  --tls-key /etc/boxy/server-key.pem \
  --tls-ca /etc/boxy/ca-cert.pem
```

**Agent Host (Windows)**:

```bash
# Start agent
boxy agent serve \
  --agent-id windows-host-01 \
  --server-url https://boxy-server:8443 \
  --cert C:\boxy\windows-01-cert.pem \
  --key C:\boxy\windows-01-key.pem \
  --ca C:\boxy\ca-cert.pem \
  --providers hyperv \
  --listen :8444
```

## Troubleshooting

### Common Issues

**1. Agent can't connect to server**

```bash
# Check network connectivity
telnet boxy-server 8443

# Check certificate
openssl s_client -connect boxy-server:8443 \
  -cert agent-cert.pem \
  -key agent-key.pem \
  -CAfile ca-cert.pem
```

**2. Certificate verification failed**

- Ensure CA cert is correct on both server and agent
- Check certificate hasn't expired: `openssl x509 -in cert.pem -noout -dates`
- Verify cert is signed by CA: `openssl verify -CAfile ca-cert.pem agent-cert.pem`

**3. Provider not found**

- Check agent registered with correct providers
- Verify pool config specifies correct `backend_agent`
- Check agent logs for registration errors

**4. Slow provisioning**

- Check network latency between server and agent
- Monitor gRPC metrics
- Consider agent placement closer to server

## Next Steps

1. Review [ADR-004](../decisions/adr-004-distributed-agent-architecture.md) for architecture decisions
2. Implement Phase 1 (Foundation)
3. Write comprehensive tests as you go
4. Update this doc with learnings and best practices
5. Create runbook for operations team
