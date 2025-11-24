# Agent Package

The `agent` package provides a gRPC server implementation that runs on remote machines to expose local provider resources (Hyper-V, Docker, etc.) to a central Boxy service.

## Overview

The agent acts as a bridge between the central Boxy service and local virtualization/containerization providers. It:

- Runs on remote machines (Windows for Hyper-V, Linux for Docker/KVM)
- Exposes local providers via gRPC with optional mTLS
- Routes RPC calls to registered provider implementations
- Manages provider lifecycle and health checks

## Architecture

```txt
Central Boxy Service
        |
        | gRPC (with mTLS)
        |
    Agent Server (this package)
        |
        +-- Docker Provider
        +-- Hyper-V Provider
        +-- Mock Provider
```

## Usage

### Starting an Agent Server

```go
import (
    "github.com/Geogboe/boxy/pkg/agent"
    "github.com/Geogboe/boxy/pkg/provider/docker"
    "github.com/sirupsen/logrus"
)

// Create agent configuration
cfg := &agent.Config{
    AgentID:     "my-agent-01",
    ListenAddr:  ":50051",
    TLSCertPath: "/path/to/agent.crt",
    TLSKeyPath:  "/path/to/agent.key",
    TLSCAPath:   "/path/to/ca.crt",
    UseTLS:      true,
}

// Create server
logger := logrus.New()
srv, err := agent.NewServer(cfg, logger)
if err != nil {
    log.Fatal(err)
}

// Register providers
dockerProvider, _ := docker.NewProvider(logger, encryptor)
srv.RegisterProvider("docker", dockerProvider)

// Start serving
if err := srv.Start(); err != nil {
    log.Fatal(err)
}
```

### CLI Usage

```bash
# Start agent with Docker + scratch/shell providers (insecure, for testing)
boxy agent serve --listen :50051 --providers docker,scratch/shell

# Start agent with mTLS (production)
boxy agent serve --listen :50051 \
  --providers hyperv \
  --tls-cert /path/to/agent.crt \
  --tls-key /path/to/agent.key \
  --tls-ca /path/to/ca.crt \
  --use-tls

# Auto-detect providers (default): Windows → hyperv + scratch/shell; Linux → docker + scratch/shell
boxy agent serve --listen :50051
```

## Configuration

### Config Struct

```go
type Config struct {
    AgentID      string  // Unique identifier for this agent
    ListenAddr   string  // Address to listen on (host:port)
    TLSCertPath  string  // Path to server certificate
    TLSKeyPath   string  // Path to server key
    TLSCAPath    string  // Path to CA certificate (for client verification)
    UseTLS       bool    // Enable TLS
}
```

### Provider Registration

Providers must implement the `provider.Provider` interface:

```go
type Provider interface {
    Provision(ctx context.Context, spec ResourceSpec) (*Resource, error)
    Destroy(ctx context.Context, res *Resource) error
    GetStatus(ctx context.Context, res *Resource) (*ResourceStatus, error)
    GetConnectionInfo(ctx context.Context, res *Resource) (*ConnectionInfo, error)
    Exec(ctx context.Context, res *Resource, cmd []string) (*ExecResult, error)
    Update(ctx context.Context, res *Resource, updates ResourceUpdate) error
    HealthCheck(ctx context.Context) error
    Name() string
    Type() ResourceType
}
```

## Security

### mTLS (Mutual TLS)

For production deployments, always use mTLS:

- **Agent**: Requires certificate signed by CA
- **Client**: Must present valid client certificate signed by same CA
- **CA**: Shared certificate authority for trust

### Certificate Generation

```bash
# Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -key ca.key -out ca.crt -days 365

# Generate agent certificate
openssl genrsa -out agent.key 4096
openssl req -new -key agent.key -out agent.csr
openssl x509 -req -in agent.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out agent.crt -days 365
```

## RPC Methods

The agent implements the `ProviderService` gRPC interface:

| Method              | Description                                       |
| ------------------- | ------------------------------------------------- |
| `Provision`         | Create a new resource (VM/container)              |
| `Destroy`           | Remove a resource                                 |
| `GetStatus`         | Get resource health and metrics                   |
| `GetConnectionInfo` | Get SSH/RDP connection details                    |
| `Exec`              | Execute command inside resource                   |
| `Update`            | Apply updates (power state, resources, snapshots) |
| `HealthCheck`       | Verify provider health                            |

## Type Conversions

The `convert.go` file handles conversion between:

- Internal provider types (`provider.Resource`, `provider.ResourceSpec`)
- Protocol buffer types (`pb.Resource`, `pb.ResourceSpec`)

This ensures clean separation between wire format and internal representation.

## Error Handling

The agent translates provider errors to gRPC status codes:

- `codes.NotFound` - Provider not registered
- `codes.Internal` - Provider operation failed
- `codes.DeadlineExceeded` - Operation timeout

## Monitoring

The agent logs:

- Provider registration events
- RPC request/response (debug level)
- Operation durations
- Error details

Use structured logging with fields:

```go
logger.WithFields(logrus.Fields{
    "agent_id": agentID,
    "provider": providerName,
    "resource_id": resourceID,
}).Info("Operation completed")
```

## Testing

See `server_test.go` for examples of:

- Mock provider registration
- RPC call testing
- Error handling validation
- Connection management

## Related Documentation

- [Distributed Agent Architecture](../../docs/architecture/distributed-agent-implementation.md)
- [ADR-004: Distributed Agent Architecture](../../docs/decisions/adr-004-distributed-agent-architecture.md)
- [Remote Provider](../provider/remote/README.md)
- [Provider Interface](../provider/README.md)
