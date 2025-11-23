# pkg/provider/docker

Docker provider implementation for Boxy.

## Purpose

Implements the `provider.Provider` interface for Docker containers, enabling Boxy to provision and manage containerized resources.

## Contract

**Implements**: `provider.Provider`

**Input**:

- `provider.ResourceSpec` - Container specification (image, resources, labels, environment)
- Context for cancellation/timeout

**Output**:

- `provider.Resource` - Provisioned container with connection info
- Encrypted credentials in resource metadata

**Guarantees**:

- Containers labeled with `boxy.managed=true`
- Random secure passwords using `pkg/crypto`
- Automatic image pulling if not present
- Resource limits enforced (CPU, memory)
- Clean teardown on provision failure

**Limitations**:

- Requires Docker daemon accessible via environment (DOCKER_HOST)
- Single container per resource (no compose/multi-container)
- Update() not yet implemented (v2 feature)

## Usage Example

```go
import (
    "context"
    "github.com/sirupsen/logrus"
    "boxy/pkg/crypto"
    "boxy/pkg/provider"
    "boxy/pkg/provider/docker"
)

// Create provider
logger := logrus.New()
key, _ := crypto.GenerateKey()
encryptor, _ := crypto.NewEncryptor(key)

provider, err := docker.NewProvider(logger, encryptor)
if err != nil {
    log.Fatal(err)
}

// Provision container
spec := provider.ResourceSpec{
    Type:         provider.ResourceTypeContainer,
    ProviderType: "docker",
    Image:        "ubuntu:22.04",
    CPUs:         2,
    MemoryMB:     4096,
    Labels: map[string]string{
        "env": "test",
    },
}

resource, err := provider.Provision(context.Background(), spec)
if err != nil {
    log.Fatal(err)
}

// Get connection info
conn, err := provider.GetConnectionInfo(context.Background(), resource)
fmt.Printf("Container: %s\n", conn.Host)
fmt.Printf("Password: %s\n", conn.Password)

// Cleanup
provider.Destroy(context.Background(), resource)
```

## Architecture

**Links:**

- [Provider Interface](../provider.go)
- [Package Reorganization](../../../docs/planning/REORGANIZATION_STATUS.md)

**Dependencies:**

- `github.com/docker/docker/client` - Docker SDK
- `pkg/provider` - Provider interface & types
- `pkg/crypto` - Password generation & encryption

**Used by:**

- `internal/core/pool` - Pool management
- `internal/core/sandbox` - Sandbox orchestration

## Features

### Provisioning

- Pulls images automatically if not present
- Sets resource limits (CPU, memory)
- Generates secure random passwords
- Encrypts passwords before storage
- Custom labels and environment variables
- Automatic IP address detection

### Destruction

- Force removes containers
- Removes volumes automatically
- Handles already-stopped containers

### Status

- Maps Docker states to resource states
- Health checks via container inspection
- Optional stats collection (CPU, memory)

### Connection

- Returns IP address and credentials
- Decrypts passwords on-demand
- Exposed port mapping

### Execution

- Run commands inside containers via `docker exec`
- Captures stdout/stderr
- Returns exit codes

## Testing

### Unit Tests

TODO: Add unit tests with mocked Docker SDK

```bash
go test -v ./pkg/provider/docker
```

### Integration Tests

Requires Docker daemon:

```bash
# Start Docker daemon
sudo systemctl start docker

# Run integration tests
go test -v ./pkg/provider/docker -tags=integration

# Or with testing.Short() skip:
go test ./pkg/provider/docker  # Includes integration
go test -short ./pkg/provider/docker  # Skips integration
```

**Test coverage needed:**

- [ ] Unit tests (mock Docker SDK)
- [ ] Integration tests (real Docker)
- [ ] Error scenarios (network issues, image pull failures)
- [ ] Resource limit enforcement
- [ ] Password encryption/decryption

## Development

### Running Tests Locally

```bash
# Unit tests (when created)
go test ./pkg/provider/docker

# Integration tests
docker pull ubuntu:22.04
go test ./pkg/provider/docker
```

### Debugging

Enable debug logging:

```go
logger := logrus.New()
logger.SetLevel(logrus.DebugLevel)
provider, _ := docker.NewProvider(logger, encryptor)
```

## Future Enhancements

- [ ] Implement Update() for resource limit changes
- [ ] Support for Docker Compose multi-container setups
- [ ] Network isolation between sandboxes
- [ ] Volume management
- [ ] Custom entrypoints and commands
- [ ] Health check configuration
- [ ] Container restart policies
