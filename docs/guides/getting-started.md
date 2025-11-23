# Getting Started with Boxy

This guide will walk you through setting up and using Boxy for the first time.

## Prerequisites

- **Docker**: Boxy MVP uses Docker as its backend provider
- **Go 1.21+**: For building from source
- **Linux/macOS**: Currently tested on Linux (Windows support coming in Phase 2+)

## Installation

### Option 1: Build from Source

```bash
git clone https://github.com/Geogboe/boxy
cd boxy
go build -o boxy ./cmd/boxy
sudo mv boxy /usr/local/bin/
```

### Option 2: Go Install (future)

```bash
go install github.com/Geogboe/boxy/cmd/boxy@latest
```

## Step-by-Step Setup

### 1. Verify Docker is Running

```bash
docker ps
# Should show running containers (or empty list if none running)
```

### 2. Initialize Boxy

```bash
boxy init
```

This creates:

- `~/.config/boxy/` directory
- `~/.config/boxy/boxy.yaml` configuration file

### 3. Customize Your Configuration

Edit `~/.config/boxy/boxy.yaml`:

```yaml
pools:
  # Start with a small pool for testing
  - name: test-containers
    type: container
    backend: docker
    image: alpine:latest
    min_ready: 2        # Keep 2 containers ready
    max_total: 5
    cpus: 1
    memory_mb: 128
    health_check_interval: 30s
```

### 4. Start the Service

Open a terminal and run:

```bash
boxy serve
```

You should see:

```text
INFO Starting Boxy service
INFO Storage initialized    db_path=/root/.config/boxy/boxy.db
INFO Docker provider registered
INFO Docker daemon is healthy
INFO Pool manager started    pool=test-containers
✓ Boxy service is running
  • 1 pools active
  • Database: /root/.config/boxy/boxy.db

Press Ctrl+C to stop
```

**What's happening:**

- Boxy connects to Docker
- Starts pool managers
- Provisions 2 alpine containers (min_ready=2)
- Keeps them running and ready

### 5. Check Pool Status

Open a new terminal:

```bash
boxy pool ls
```

Output:

```text
Resource Pools:

NAME              TYPE       BACKEND  IMAGE          READY  ALLOCATED  MIN  MAX  HEALTHY
test-containers   container  docker   alpine:latest  2      0          2    5    ✓
```

### 6. Create Your First Sandbox

```bash
boxy sandbox create \
  --pool test-containers:1 \
  --duration 10m \
  --name my-first-sandbox
```

Output:

```text
Creating sandbox...

✓ Sandbox created successfully

ID:         abc123...
Name:       my-first-sandbox
Resources:  1
Expires:    2025-11-17T19:15:00Z (in 10m0s)

Resource Connection Info:
─────────────────────────

[1] Resource def456...
    Type: docker-exec
    Host: 172.17.0.2
    Username: root
    Password: xK9pL2mN5qR8
    Container: a1b2c3d4e5f6
    Connect: docker exec -it a1b2c3d4e5 /bin/bash
```

### 7. Connect to Your Sandbox

```bash
# Use the container ID from the output above
docker exec -it a1b2c3d4e5 /bin/bash
```

You're now inside an Alpine container!

### 8. Watch Auto-Replenishment

In the serve terminal, you'll see:

```text
INFO Resource allocated       pool=test-containers resource_id=def456...
INFO Replenishing pool        pool=test-containers needed=1 available=1 min_ready=2
INFO Resource provisioned and ready   pool=test-containers resource_id=ghi789...
```

Check the pool again:

```bash
boxy pool ls
```

Now shows:

```text
NAME              READY  ALLOCATED  ...
test-containers   2      1          ...
```

The pool auto-replenished to maintain min_ready=2!

### 9. List Active Sandboxes

```bash
boxy sandbox ls
```

Output:

```text
Active Sandboxes:

ID        NAME              RESOURCES  CREATED   EXPIRES   TIME REMAINING
abc123    my-first-sandbox  1          19:05:00  19:15:00  9m45s
```

### 10. Cleanup

#### Manual Destruction

```bash
boxy sandbox destroy abc123
```

#### Automatic Expiration

Just wait 10 minutes - Boxy will automatically:

1. Mark sandbox as expired
2. Destroy allocated resources
3. Replenish the pool

## Common Patterns

### Development Environment

```yaml
pools:
  - name: dev-ubuntu
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 3
    max_total: 10
    cpus: 2
    memory_mb: 1024
```

```bash
# Create dev environment
boxy sandbox create -p dev-ubuntu:1 -d 8h -n my-dev-env

# Get connection details
boxy sandbox ls

# Work in it all day (8 hour duration)
# Auto-destroys at end of day
```

### Testing Environment

```yaml
pools:
  - name: test-runners
    type: container
    backend: docker
    image: node:18-alpine
    min_ready: 5      # Always have 5 ready
    max_total: 20
    cpus: 2
    memory_mb: 512
```

```bash
# In your CI pipeline
SANDBOX_ID=$(boxy sandbox create -p test-runners:1 -d 15m --json | jq -r '.id')
# Run tests in sandbox
boxy sandbox destroy $SANDBOX_ID
```

### Multi-Container Sandbox

```yaml
pools:
  - name: app-servers
    type: container
    backend: docker
    image: node:18
    min_ready: 2
    max_total: 10

  - name: databases
    type: container
    backend: docker
    image: postgres:15
    min_ready: 1
    max_total: 5
    environment:
      POSTGRES_PASSWORD: secret
```

```bash
# Create full stack
boxy sandbox create \
  -p app-servers:2 \
  -p databases:1 \
  -d 4h \
  -n full-stack-test
```

## Troubleshooting

### "Docker daemon not reachable"

Ensure Docker is running:

```bash
sudo systemctl start docker
docker ps
```

### "Pool not found"

Check your config has the pool defined:

```bash
cat ~/.config/boxy/boxy.yaml
```

### "No resources available"

Pool might be at capacity:

```bash
boxy pool stats <pool-name>
```

Check if `Total >= MaxTotal`. Either:

1. Increase `max_total` in config
2. Destroy some sandboxes
3. Wait for expired sandboxes to clean up

### "Config file not found"

Run `boxy init` first to create the config.

## Next Steps

- Read the [CLI Reference](../README.md#cli-reference) for all commands
- Learn about [Pool Configuration](../README.md#pool-configuration)
- Explore the [Roadmap](../ROADMAP.md) for upcoming features
- Check out [Architecture](../architecture/tech-stack-research.md)

## Tips

1. **Start Small**: Begin with `min_ready: 1` or `2` to avoid overwhelming your system
2. **Use Alpine**: For testing, Alpine images are tiny (~5MB) and fast to provision
3. **Monitor Logs**: Keep `boxy serve` running in a visible terminal to see activity
4. **Short Durations**: Use short durations (15m, 30m) while testing to avoid resource buildup
5. **Clean Slate**: Delete `~/.config/boxy/boxy.db` to reset all state

**You're ready to start using Boxy! 🎉**
