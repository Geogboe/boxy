# Getting Started with Boxy

This guide will walk you through setting up and using Boxy for the first time. It covers installation, basic usage, configuration, and troubleshooting.

## Prerequisites

- **Docker**: Boxy primarily uses Docker as its backend provider for container resources.
  - Linux: Install Docker Engine
  - macOS: Install Docker Desktop
  - Windows: Install Docker Desktop (with WSL2)
- **Go 1.21+**: For building Boxy from source.
- **Linux/macOS**: Currently tested on Linux and macOS. (Windows support for running the main Boxy service is not yet fully documented, but agents can run on Windows.)

## Installation

### Option 1: Install from Binary (Recommended for users)

Download the latest release for your platform from the [GitHub Releases page](https://github.com/Geogboe/boxy/releases).
Replace `boxy-linux-amd64` with the appropriate file for your system (e.g., `boxy-darwin-arm64` for Apple Silicon Mac).

```bash
# Linux / macOS (adjust version and platform as needed)
curl -L https://github.com/Geogboe/boxy/releases/latest/download/boxy-linux-amd64 -o boxy
chmod +x boxy
sudo mv boxy /usr/local/bin/

# Verify installation
boxy --version
```

### Option 2: Build from Source (For developers)

```bash
git clone https://github.com/Geogboe/boxy.git
cd boxy
go build -o boxy ./cmd/boxy
sudo mv boxy /usr/local/bin/
```

### Option 3: Go Install (For developers)

```bash
go install github.com/Geogboe/boxy/cmd/boxy@latest
```

### Option 4: Run with Docker Compose (For quick testing/development)

This option allows you to run Boxy and its dependencies (like a database) within Docker containers.

```bash
git clone https://github.com/Geogboe/boxy.git
cd boxy
docker-compose up -d
```

## Step-by-Step Setup

### 1. Verify Docker is Running

Boxy relies on Docker for container provisioning. Ensure your Docker daemon is active.

```bash
docker ps
# Should show running containers (or an empty list if none running)
```

### 2. Initialize Boxy

Run the `boxy init` command. This will create the necessary configuration directory and a default `boxy.yaml` file.

```bash
boxy init
```

This creates:

-   `~/.config/boxy/` directory
-   `~/.config/boxy/boxy.yaml` configuration file
-   On first run, Boxy will also auto-generate an encryption key in `~/.config/boxy/encryption.key` (see Security section).

### 3. Customize Your Configuration

Edit the generated configuration file at `~/.config/boxy/boxy.yaml`.

A basic configuration looks like this:

```yaml
# Storage configuration: Boxy uses SQLite for its internal state
storage:
  type: sqlite
  path: ~/.config/boxy/boxy.db # Path to the SQLite database file

# Logging configuration
logging:
  level: info # Log level: debug, info, warn, error
  format: text # Log format: text or json

# Define your resource pools
pools:
  # Start with a small pool for testing
  - name: test-containers
    type: container
    backend: docker
    image: alpine:latest # Use a lightweight image for quick testing
    min_ready: 2         # Keep 2 containers ready for instant allocation
    max_total: 5         # Maximum 5 containers that can be managed by this pool
    cpus: 1
    memory_mb: 128
    health_check_interval: 30s # How often to check health of resources
```

### 4. Start the Boxy Service

Open a terminal and run the Boxy service:

```bash
boxy serve
```

You should see output similar to this:

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
-   Boxy connects to your Docker daemon.
-   It initializes its internal storage (SQLite database).
-   It starts the pool managers configured in `boxy.yaml`.
-   It provisions `min_ready` (e.g., 2) Alpine containers, keeping them running and ready for allocation.

### 5. Check Pool Status

Open a **new** terminal and check the status of your configured pools:

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

Now, create a sandbox from the `test-containers` pool:

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

Use the connection command provided in the output above (replace `a1b2c3d4e5` with your actual container ID):

```bash
docker exec -it a1b2c3d4e5 /bin/bash
```

You're now inside your ephemeral Alpine container!

### 8. Watch Auto-Replenishment

Observe the terminal where `boxy serve` is running. You'll see:

```text
INFO Resource allocated       pool=test-containers resource_id=def456...
INFO Replenishing pool        pool=test-containers needed=1 available=1 min_ready=2
INFO Resource provisioned and ready   pool=test-containers resource_id=ghi789...
```

Check the pool status again in your new terminal:

```bash
boxy pool ls
```

Now it shows that the pool auto-replenished to maintain `min_ready=2`:

```text
NAME              READY  ALLOCATED  ...
test-containers   2      1          ...
```

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

You can manually destroy an active sandbox:

```bash
boxy sandbox destroy abc123
```

#### Automatic Expiration

If you wait for the `duration` you specified (e.g., 10 minutes), Boxy will automatically:
1.  Mark the sandbox as expired.
2.  Destroy its allocated resources.
3.  Replenish the pool.

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

## Configuration

### Environment Variables

You can override settings in the `boxy.yaml` configuration file using environment variables. This is useful for CI/CD pipelines or deployment environments.

```bash
# Storage
export BOXY_STORAGE_TYPE=sqlite
export BOXY_STORAGE_PATH=/path/to/boxy.db

# Logging
export BOXY_LOGGING_LEVEL=debug  # debug, info, warn, error
export BOXY_LOGGING_FORMAT=json  # text or json

# Encryption (optional - auto-generated if not set)
export BOXY_ENCRYPTION_KEY=$(cat ~/.config/boxy/encryption.key)
```

### Security: Encryption Key

Boxy automatically encrypts sensitive credentials (like passwords) stored in its database.

-   **First run**: If no encryption key is provided, Boxy will auto-generate a 32-byte key and save it to `~/.config/boxy/encryption.key`.
-   **Permissions**: The key file is created with secure `0600` permissions (owner read/write only).
-   **Environment variable**: You can override the key by setting the `BOXY_ENCRYPTION_KEY` environment variable with a base64-encoded 32-byte key.

**Important**: **Backup your encryption key!** Without it, you will **not** be able to decrypt stored passwords or access sandboxes that rely on those credentials. Treat this key like a password manager master key.

```bash
# Example: Backup encryption key
cp ~/.config/boxy/encryption.key ~/boxy-encryption-key.backup

# Example: Set in environment for easier backup or CI/CD
export BOXY_ENCRYPTION_KEY=$(cat ~/.config/boxy/encryption.key | base64)
```

## Running as a System Service

For production deployments, it\'s recommended to run Boxy as a system service.

### Linux (systemd)

1.  **Create Boxy User**: For security, run Boxy under its own unprivileged user.
    ```bash
    sudo useradd -r -s /bin/false boxy
    sudo usermod -aG docker boxy # Add boxy user to docker group
    ```
2.  **Copy Configuration**: Copy your `boxy.yaml` and `encryption.key` to the system user\'s config path.
    ```bash
    sudo mkdir -p /home/boxy/.config/boxy
    sudo cp ~/.config/boxy/boxy.yaml /home/boxy/.config/boxy/
    sudo cp ~/.config/boxy/encryption.key /home/boxy/.config/boxy/ # IMPORTANT: copy your key!
    sudo chown -R boxy:boxy /home/boxy/.config/boxy
    ```
3.  **Create systemd Service File**: Create `/etc/systemd/system/boxy.service` with the following content:
    ```ini
    [Unit]
    Description=Boxy Sandbox Orchestration Service
    After=docker.service
    Requires=docker.service

    [Service]
    Type=simple
    User=boxy
    Group=boxy
    WorkingDirectory=/home/boxy
    ExecStart=/usr/local/bin/boxy serve
    Restart=on-failure
    RestartSec=5s
    StandardOutput=journal
    StandardError=journal

    # Security Enhancements (Recommended)
    NoNewPrivileges=true
    PrivateTmp=true
    ProtectSystem=strict
    ProtectHome=true
    ReadWritePaths=/home/boxy/.config/boxy /var/lib/boxy # Adjust /var/lib/boxy if using default db path

    [Install]
    WantedBy=multi-user.target
    ```
    *Note: Adjust `ReadWritePaths` if your `boxy.db` is stored elsewhere.*

4.  **Enable and Start Service**:
    ```bash
    sudo systemctl daemon-reload
    sudo systemctl enable boxy
    sudo systemctl start boxy
    ```
5.  **Check Status and Logs**:
    ```bash
    sudo systemctl status boxy
    sudo journalctl -u boxy -f
    ```

### macOS (launchd)

1.  **Create launchd Agent File**: Create `~/Library/LaunchAgents/com.boxy.service.plist` with the following content:
    ```xml
    <?xml version="1.0" encoding="UTF-8"?>
    <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
    <plist version="1.0">
    <dict>
        <key>Label</key>
        <string>com.boxy.service</string>
        <key>ProgramArguments</key>
        <array>
            <string>/usr/local/bin/boxy</string>
            <string>serve</string>
        </array>
        <key>RunAtLoad</key>
        <true/>
        <key>KeepAlive</key>
        <true/>
        <key>StandardOutPath</key>
        <string>/tmp/boxy.log</string>
        <key>StandardErrorPath</key>
        <string>/tmp/boxy.error.log</string>
    </dict>
    </plist>
    ```
2.  **Load and Start Agent**:
    ```bash
    launchctl load ~/Library/LaunchAgents/com.boxy.service.plist
    launchctl start com.boxy.service
    ```

## Troubleshooting

### Docker Connection Issues

-   **"Docker daemon not reachable"** or similar errors.
    ```bash
    # Check if Docker is running
    sudo systemctl start docker # On Linux
    docker ps

    # Verify user has Docker permissions
    groups # Should show 'docker' group
    # If not, add user to docker group and re-login or run:
    # sudo usermod -aG docker $USER && newgrp docker
    ```

### Orphaned Containers

If Boxy crashes unexpectedly, some containers managed by Boxy might be left running.
```bash
# List Boxy-managed containers
docker ps -a --filter "label=boxy.managed=true"

# Clean up orphaned containers
docker rm -f $(docker ps -aq --filter "label=boxy.managed=true")
```

### Encryption Key Lost

If you lose your `encryption.key`, you will **not** be able to decrypt existing passwords for sandboxes.
**Recovery involves data loss:**
1.  Stop Boxy service.
2.  Delete database: `rm ~/.config/boxy/boxy.db`.
3.  Delete encryption key: `rm ~/.config/boxy/encryption.key`.
4.  Restart Boxy (it will regenerate the key and recreate the database).

**This process will destroy all existing sandboxes and resources, as their metadata is lost.**

### Resource Pools Not Replenishing

-   Check pool status: `boxy pool ls`. If pools show errors.
-   Ensure Docker images exist: `docker pull ubuntu:22.04`.
-   Check Docker has sufficient resources (memory, disk).
-   View logs for `boxy serve`: `sudo journalctl -u boxy -f` (if running as systemd service).

### "Config file not found"

Run `boxy init` first to create the default config directory and file.

### Verbose Logging

Enable debug logging for more detailed output:
```bash
# Via environment variable
BOXY_LOGGING_LEVEL=debug boxy serve

# Or in config file (boxy.yaml)
logging:
  level: debug
  format: text
```

## Next Steps

-   Read the [CLI Reference](/README.md#cli-reference) for all commands (Note: This link assumes `README.md` at root has a CLI reference, otherwise adjust.)
-   Explore the [Roadmap](../ROADMAP.md) for upcoming features.
-   Check out [Examples](/examples/) for common use cases.
-   Learn about [Boxy\'s Architecture](../architecture/overview.md) to understand how it works.
-   **For distributed setups (Linux server + Windows agents), see the [Distributed Quick Start Guide](../QUICK_START_DISTRIBUTED.md).**

## Tips

1.  **Start Small**: Begin with `min_ready: 1` or `2` to avoid overwhelming your system initially.
2.  **Use Alpine**: For testing, Alpine images are tiny (~5MB) and fast to provision.
3.  **Monitor Logs**: Keep `boxy serve` running in a visible terminal to observe activity.
4.  **Short Durations**: Use short durations (e.g., `15m`, `30m`) while testing to avoid resource buildup.
5.  **Clean Slate**: Delete `~/.config/boxy/boxy.db` to reset all state (losing all sandbox info).

**You\'re ready to start using Boxy! 🎉**