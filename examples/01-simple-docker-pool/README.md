# Example 1: Simple Docker Pool

This is the simplest possible Boxy setup - a single Docker pool that maintains ready containers.

## What This Demonstrates

- Basic pool configuration
- Automatic pool warming (maintains `min_ready` containers)
- Creating and destroying sandboxes
- Connection info retrieval

## Prerequisites

- Docker installed and running
- Boxy binary built

## Files

- `boxy.yaml` - Configuration file
- `run.sh` - Start the Boxy service
- `test.sh` - Create and destroy a sandbox

## Quick Start

```bash
# 1. Start Boxy service
./run.sh

# 2. In another terminal, test it
./test.sh

# 3. Check pool status
boxy pool list
boxy pool stats simple-pool
```

## How It Works

1. **Pool Warming**: Boxy immediately provisions 2 containers (min_ready)
2. **Allocation**: When you create a sandbox, Boxy allocates a ready container
3. **Replenishment**: After allocation, Boxy provisions a new container to maintain min_ready
4. **Cleanup**: Expired sandboxes are automatically destroyed

## Expected Output

```text
# Pool list shows pool status
simple-pool    container    2/2 ready    0 allocated

# Sandbox creation is fast (container already warm)
Sandbox created: sb-abc123
Connection: docker-exec into container-xyz

# Pool automatically replenishes
simple-pool    container    1/2 ready    1 allocated
simple-pool    container    2/2 ready    1 allocated  (after replenishment)
```

## Configuration Explained

```yaml
pools:
  - name: simple-pool
    type: container
    backend: docker           # Uses local Docker provider
    image: ubuntu:22.04       # Base image
    min_ready: 2              # Always keep 2 containers ready
    max_total: 5              # Never exceed 5 total containers
    cpus: 1
    memory_mb: 512
```

This maintains a warm pool of 2 Ubuntu containers, ready for instant allocation.

## Next Steps

- Try `examples/02-hooks-demo` to see hook-based provisioning
- Try `examples/03-remote-agent` to see distributed architecture
