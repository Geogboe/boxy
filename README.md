# Boxy 🎁

**Sandboxing orchestration tool for mixed virtual environments with automatic lifecycle management.**

Boxy simplifies spinning up VMs, containers, and processes across different platforms with warm pools for instant allocation. Define your resource pools once, and Boxy keeps them ready - allocate resources on-demand, use them, and let them auto-expire.

## Core Concept

**Problem**: Creating mixed environments (VMs + containers) is manual and slow.
**Solution**: Boxy maintains **warm pools** of pre-provisioned resources that auto-replenish.
**Result**: Request → Instant allocation → Use → Auto-cleanup.

## Features

✅ **Warm Pools** - Resources always ready (configurable min_ready count)
✅ **Auto-Replenishment** - Background workers maintain pool levels
✅ **Multi-Backend** - Docker, Hyper-V, KVM, VMware (compiled-in providers)
✅ **Automatic Cleanup** - Sandboxes auto-expire after duration
✅ **Health Monitoring** - Continuous health checks on pooled resources
✅ **Simple CLI** - Easy-to-use command-line interface
✅ **Lifecycle Management** - Full resource lifecycle from provision to destroy

## Quick Start

### 1. Install

```bash
# Build from source
git clone https://github.com/Geogboe/boxy
cd boxy
go build -o boxy ./cmd/boxy

# Move to PATH
sudo mv boxy /usr/local/bin/
```

### 2. Initialize Configuration

```bash
boxy init
# Creates ~/.config/boxy/boxy.yaml with example pools
```

### 3. Edit Configuration

```yaml
# ~/.config/boxy/boxy.yaml
storage:
  type: sqlite
  path: ~/.config/boxy/boxy.db

logging:
  level: info
  format: text

pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 3      # Always keep 3 containers ready
    max_total: 10     # Maximum 10 total
    cpus: 2
    memory_mb: 512
    health_check_interval: 30s
```

### 4. Start the Service

```bash
# Terminal 1: Run the service (keeps pools warm)
boxy serve

# The service will:
# - Start all configured pools
# - Provision min_ready containers
# - Monitor health continuously
# - Auto-replenish when resources are allocated
# - Clean up expired sandboxes
```

### 5. Use It!

```bash
# Terminal 2: Check pool status
boxy pool ls

# Create a sandbox with 2 containers
boxy sandbox create \
  --pool ubuntu-containers:2 \
  --duration 1h \
  --name my-dev-env

# List active sandboxes
boxy sandbox ls

# Destroy sandbox when done
boxy sandbox destroy <sandbox-id>
```

## Architecture

```
┌─────────────────────────────────────────┐
│           CLI / User Interface           │
└─────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────┐
│          Boxy Service Core               │
│  ┌───────────────────────────────────┐  │
│  │ Pool Managers (warm pools)        │  │
│  │  • Background replenishment       │  │
│  │  • Health checking                │  │
│  │  • Resource allocation            │  │
│  └───────────────────────────────────┘  │
│  ┌───────────────────────────────────┐  │
│  │ Sandbox Manager                   │  │
│  │  • Resource orchestration         │  │
│  │  • Automatic cleanup              │  │
│  └───────────────────────────────────┘  │
└─────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────┐
│       Backend Providers (compiled-in)    │
│   Docker │ Hyper-V │ KVM │ VMware        │
└─────────────────────────────────────────┘
```

## Use Cases

### Development Environments
```bash
# Spin up a full dev stack instantly
boxy sandbox create \
  --pool ubuntu-containers:3 \
  --pool nginx-containers:1 \
  --duration 8h \
  --name dev-stack
```

### Testing & CI/CD
```bash
# Fresh environment for each test run
boxy sandbox create --pool alpine-containers:1 --duration 30m
# Run tests
# Auto-destroys after 30 minutes
```

### Security Research
```bash
# Isolated lab environment
boxy sandbox create \
  --pool win-server-vms:3 \
  --pool win-client-vms:2 \
  --duration 4h \
  --name malware-analysis-lab
```

## CLI Reference

### Global Flags
```
--config string      Config file path (default: ~/.config/boxy/boxy.yaml)
--db string          Database path (default: ~/.config/boxy/boxy.db)
--log-level string   Log level: debug, info, warn, error (default: info)
```

### Commands

#### `boxy init`
Initialize configuration file

```bash
boxy init                  # Create config at default location
boxy init --force          # Overwrite existing config
```

#### `boxy serve`
Start the Boxy service with warm pool maintenance

```bash
boxy serve                 # Start with default config
boxy serve --log-level=debug  # Start with debug logging
```

**What it does:**
- Starts all configured pools
- Provisions min_ready resources for each pool
- Runs background workers:
  - Replenishment worker (maintains min_ready)
  - Health check worker (monitors resources)
  - Cleanup worker (destroys expired sandboxes)
- Graceful shutdown on Ctrl+C

#### `boxy pool`
Manage resource pools

```bash
boxy pool ls               # List all pools
boxy pool stats <name>     # Detailed stats for a pool
```

**Output example:**
```
NAME                TYPE       BACKEND  IMAGE          READY  ALLOCATED  MIN  MAX  HEALTHY
ubuntu-containers   container  docker   ubuntu:22.04   3      0          3    10   ✓
alpine-containers   container  docker   alpine:latest  5      0          5    20   ✓
```

#### `boxy sandbox`
Manage sandboxes

```bash
# Create sandbox
boxy sandbox create \
  --pool <pool-name>:<count> \    # Can specify multiple times
  [--name <name>] \
  [--duration <duration>] \        # Default: 2h
  [--json]                          # Output JSON

# List sandboxes
boxy sandbox ls

# Destroy sandbox
boxy sandbox destroy <sandbox-id>
```

**Examples:**
```bash
# Single pool
boxy sandbox create -p ubuntu-containers:1 -d 30m

# Multiple pools
boxy sandbox create \
  -p ubuntu-containers:2 \
  -p nginx-containers:1 \
  -d 2h \
  -n web-test-env

# Get JSON output for automation
boxy sandbox create -p ubuntu-containers:1 --json
```

## Configuration Reference

### Storage Options

```yaml
storage:
  type: sqlite                    # sqlite or postgres
  path: ~/.config/boxy/boxy.db    # for sqlite
  # dsn: "postgres://..."         # for postgres (future)
```

### Pool Configuration

```yaml
pools:
  - name: pool-name               # Unique pool identifier
    type: container               # container, vm, or process
    backend: docker               # docker, hyperv, kvm, vmware
    image: ubuntu:22.04           # Image/template to use
    min_ready: 3                  # Minimum ready resources (warm pool)
    max_total: 10                 # Maximum total resources

    # Optional resource limits
    cpus: 2                       # CPU allocation
    memory_mb: 512                # Memory in MB
    disk_gb: 20                   # Disk in GB (for VMs)

    # Optional metadata
    labels:
      environment: dev
      team: backend

    # Optional environment variables
    environment:
      MY_VAR: value

    # Health check interval
    health_check_interval: 30s    # How often to health check
```

## How Warm Pools Work

1. **Initialization**: When `boxy serve` starts, each pool provisions `min_ready` resources
2. **Monitoring**: Background worker checks every 10 seconds if `ready_count < min_ready`
3. **Replenishment**: If below threshold, provisions new resources automatically
4. **Health Checks**: Unhealthy resources are destroyed and replaced
5. **Allocation**: When sandbox is created, ready resources are instantly allocated
6. **Cleanup**: When sandbox expires or is destroyed, resources are destroyed (not reused for security)

## Development Status

**Phase**: MVP (Phase 1)
**Status**: ✅ Functional

### Completed
- ✅ Core domain models (Resource, Pool, Sandbox)
- ✅ Docker backend provider
- ✅ Pool manager with warm pool maintenance
- ✅ Sandbox orchestration
- ✅ SQLite storage layer
- ✅ Full CLI (serve, pool, sandbox commands)
- ✅ Background workers (replenishment, health, cleanup)
- ✅ Automatic expiration and cleanup

### Roadmap
- [ ] **Phase 2**: Additional providers (Hyper-V, KVM)
- [ ] **Phase 3**: REST API & service daemon mode
- [ ] **Phase 4**: Web UI
- [ ] **Phase 5**: Multi-tenancy, advanced features

See [docs/ROADMAP.md](docs/ROADMAP.md) for detailed roadmap.

## Project Structure

```
boxy/
├── cmd/
│   └── boxy/              # CLI entry point
│       ├── main.go
│       └── commands/      # Cobra commands
├── internal/
│   ├── core/              # Core domain logic
│   │   ├── pool/          # Pool management
│   │   ├── sandbox/       # Sandbox orchestration
│   │   └── resource/      # Resource abstractions
│   ├── provider/          # Provider implementations
│   │   └── docker/        # Docker provider
│   ├── storage/           # State persistence (SQLite)
│   └── config/            # Configuration management
├── pkg/
│   └── provider/          # Provider interface
├── docs/                  # Documentation
│   ├── ROADMAP.md
│   ├── architecture/
│   ├── decisions/         # ADRs
│   └── guides/
└── boxy.example.yaml      # Example configuration
```

## Technology Stack

- **Language**: Go 1.21+
- **CLI Framework**: Cobra
- **Configuration**: Viper + YAML
- **Database**: SQLite (GORM)
- **Docker SDK**: Official Docker client
- **Logging**: Logrus

## Contributing

We welcome contributions! See [CLAUDE.md](CLAUDE.md) for development guidelines.

**Key Points**:
- Use conventional commits (`feat:`, `fix:`, `docs:`, etc.)
- Run tests before committing
- Add tests for new features
- Update documentation

## Architecture Decisions

See [docs/decisions/](docs/decisions/) for Architecture Decision Records (ADRs):
- [ADR-001: Technology Stack](docs/decisions/adr-001-technology-stack.md)
- [ADR-002: Provider Architecture](docs/decisions/adr-002-provider-architecture.md)
- [ADR-003: Configuration & State Storage](docs/decisions/adr-003-configuration-state-storage.md)

## License

[License TBD]

## Authors

- **geogboe** - Initial creator

## Acknowledgments

- Inspired by the complexity of managing heterogeneous virtualization environments
- Built with Go's excellent concurrency primitives for warm pool management
- Uses industry-standard libraries (Cobra, Viper, GORM, Docker SDK)

---

**Ready to simplify your sandboxing workflow?**

```bash
boxy init && boxy serve
```

Let Boxy handle the orchestration. You focus on building. 🚀
