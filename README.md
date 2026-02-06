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

## Installation

### From GitHub Releases (recommended)

Download a prebuilt binary from the [latest release](https://github.com/Geogboe/boxy/releases/latest).
Binaries are available for Linux, macOS, and Windows on amd64 and arm64.

```bash
# Example: Linux amd64
curl -LO https://github.com/Geogboe/boxy/releases/latest/download/boxy-linux-amd64
chmod +x boxy-linux-amd64
sudo mv boxy-linux-amd64 /usr/local/bin/boxy
```

Each release includes a `checksums.txt` file for verification.

### From source via `go install`

> **Note**: This is a private repository. You must configure Go and Git for private access first.

```bash
# 1. Tell Go to skip the public proxy for this module
go env -w GOPRIVATE=github.com/Geogboe/boxy

# 2. Tell Go's module fetcher to use SSH for this repo
#    (Go uses HTTPS by default even if you use SSH for git clone)
git config --global url."git@github.com:Geogboe/boxy".insteadOf "https://github.com/Geogboe/boxy"

# 3. Install
go install github.com/Geogboe/boxy/cmd/boxy@latest
```

### From source (development)

```bash
git clone git@github.com:Geogboe/boxy.git
cd boxy
go build -o boxy ./cmd/boxy
```

## Quick Start

A longer Getting Started guide is available in the docs; the sections below provide a short, practical summary — see [Getting Started with Boxy](docs/guides/getting-started.md) for full details.

1. Initialize Boxy and create a default config:

```bash
boxy init
```

1. Start the Boxy service (keeps pools warm):

```bash
boxy serve
```

1. Create a quick sandbox for testing (example):

```bash
boxy sandbox create --pool ubuntu-containers:1 --duration 10m --name quick-test
```

Use `boxy pool ls` and `boxy sandbox ls` to inspect state, and see the guide for more options.

## Pinned Development Tools

Development tooling is pinned via `tools.go` (build tag `tools`). Install the pinned binaries by targeting the tool path with `go install -tags tools`. For example:

```bash
go install -tags tools golang.org/x/vuln/cmd/govulncheck
```

Other tools provided by `tools.go` include `golang.org/x/tools/cmd/goimports`, `github.com/golangci/golangci-lint/cmd/golangci-lint`, `google.golang.org/grpc/cmd/protoc-gen-go-grpc`, and `google.golang.org/protobuf/cmd/protoc-gen-go`.

## Architecture

```text
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

```text
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

```text
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

**Phase**: v1-prerelease (Phase 1)
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

```text
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

- **Language**: Go 1.24+
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
- See [AGENTS.md](AGENTS.md) for AI assistant development guidelines

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
