# Boxy 🎁

[![CI](https://github.com/Geogboe/boxy/actions/workflows/ci.yml/badge.svg)](https://github.com/Geogboe/boxy/actions/workflows/ci.yml)
[![Release](https://github.com/Geogboe/boxy/actions/workflows/release.yml/badge.svg)](https://github.com/Geogboe/boxy/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/Geogboe/boxy)](https://goreportcard.com/report/github.com/Geogboe/boxy)
[![License](https://img.shields.io/github/license/Geogboe/boxy)](LICENSE)

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

**Option A: Download Pre-built Binary (Recommended)**

```bash
# Linux (amd64)
wget https://github.com/Geogboe/boxy/releases/latest/download/boxy-linux-amd64.tar.gz
tar -xzf boxy-linux-amd64.tar.gz
sudo mv boxy-linux-amd64 /usr/local/bin/boxy

# macOS (Apple Silicon)
wget https://github.com/Geogboe/boxy/releases/latest/download/boxy-darwin-arm64.tar.gz
tar -xzf boxy-darwin-arm64.tar.gz
sudo mv boxy-darwin-arm64 /usr/local/bin/boxy

# Windows
# Download boxy-windows-amd64.exe.zip from releases page
```

**Option B: Docker**

```bash
docker pull ghcr.io/geogboe/boxy:latest
docker run --rm ghcr.io/geogboe/boxy:latest version
```

**Option C: Build from Source**

```bash
git clone https://github.com/Geogboe/boxy
cd boxy
task build
sudo task install
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

### Primary: Quick Testing Environment
Think **Windows Sandbox** for any platform. Get a clean, isolated VM or container instantly to test software, scripts, or configurations.

```bash
# Need to test an installer? Get a clean Windows 11 VM instantly
boxy sandbox create --pool win11-test:1 --duration 1h

# If preheated → instant allocation (< 5 seconds)
# If cold → starts VM → allocates (30-60 seconds)
# Auto-destroys after 1 hour
```

**Why Boxy?**
- ✅ **Instant**: Preheated resources ready immediately
- ✅ **Clean**: Always fresh, never reused
- ✅ **Simple**: One command, no cleanup needed
- ✅ **Secure**: Complete isolation, auto-expiration

### Secondary Use Cases

**CI/CD Runners**
```bash
# Ephemeral build agents - always fresh, no contamination
boxy sandbox create --pool ci-runner:1 --duration 30m
```

**Security Red Teaming**
```bash
# Isolated malware analysis or attack simulation
boxy sandbox create \
  --pool malware-analysis:1 \
  --duration 4h \
  --name red-team-session
```

**Development Environments**
```bash
# Full dev stack (DB + web + cache)
boxy sandbox create \
  --pool postgres-db:1 \
  --pool nginx-web:1 \
  --pool redis-cache:1 \
  --duration 8h
```

**Training & Education**
```bash
# Provision lab environments for students
for i in {1..30}; do
  boxy sandbox create --pool docker-training:1 --duration 4h --name student-$i
done
```

📖 **See [docs/USE_CASES.md](docs/USE_CASES.md) for detailed use case documentation.**

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

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed development guidelines.

**Quick Start**:
```bash
# Clone and setup
git clone https://github.com/Geogboe/boxy
cd boxy
go mod download

# Run tests
task test

# Build
task build

# See all available commands
task --list
```

**Key Points**:
- Use conventional commits (`feat:`, `fix:`, `docs:`, etc.)
- Run tests before committing (`task check`)
- Add tests for new features
- Update documentation as needed
- See [CLAUDE.md](CLAUDE.md) for AI assistant development guidelines

## Releases

Releases are automated via GitHub Actions when version tags are pushed:

```bash
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

This automatically:
- Builds binaries for all platforms
- Generates changelog from commits
- Creates GitHub release
- Builds and pushes Docker images

See [docs/RELEASE.md](docs/RELEASE.md) for detailed release process.

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
