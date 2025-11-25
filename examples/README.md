# Boxy Configuration Examples

This directory contains example configurations demonstrating various Boxy features.

## Quick Start

**New to Boxy? Start here!** 👇

```bash
# Try the scratch provider (no Docker/VMs needed!)
cd examples/00-quickstart-scratch
./run.sh

# In another terminal
boxy sandbox create --pool scratch-pool:1 --duration 30m --name my-workspace
```

See [00-quickstart-scratch](./00-quickstart-scratch/) for a complete walkthrough.

## Examples

### 0. 00-quickstart-scratch/ 🚀 **START HERE**

**Simplest way to try Boxy** - No Docker or VMs required!

The scratch/shell provider creates lightweight filesystem-based workspaces.

- Zero dependencies (no Docker/Hyper-V needed)
- Instant provisioning (no images to pull)
- Perfect for learning and local testing
- Includes interactive tutorial

```bash
cd examples/00-quickstart-scratch
./run.sh
```

⚠️ Note: No isolation - not for production use.

See [00-quickstart-scratch/README.md](./00-quickstart-scratch/README.md)

### 1. simple-docker-pool.yaml

**Basic Docker pool** - Minimal configuration for getting started.

- Single pool with Ubuntu containers
- 3 containers always ready
- Maximum 10 containers total
- Good for: Learning Boxy, development

```bash
boxy serve --config examples/simple-docker-pool.yaml
```

### 2. docker-with-hooks.yaml

**Hooks demonstration** - Shows finalization and personalization hooks.

**Finalization hooks (after_provision)**:

- Install development tools
- Validate network connectivity
- Run during pool warming (slow, background)

**Personalization hooks (before_allocate)**:

- Create user account
- Set up workspace
- Run during allocation (fast, user-specific)

```bash
boxy serve --config examples/docker-with-hooks.yaml
```

### 3. multi-pool-config.yaml

**Multiple pools** - Different resource types for different use cases.

- **dev-ubuntu**: Development containers (2 CPUs, 2GB RAM)
- **build-ubuntu**: Build containers (4 CPUs, 8GB RAM)
- **test-alpine**: Testing containers (1 CPU, 512MB RAM)

Each pool has appropriate hooks for its use case.

```bash
boxy serve --config examples/multi-pool-config.yaml

# Request from specific pool
boxy sandbox create --pool build-ubuntu --duration 4h
```

### 4. 04-hyperv-local/ 🪟 **Windows Users**

**Hyper-V local deployment** - Run Boxy on Windows with local Hyper-V.

Perfect for Windows development environments with Hyper-V enabled.

- Embedded agent mode (no separate agent process)
- Hyper-V VMs with automatic credential generation
- Preheating (1 VM kept ready)
- Fast provisioning with differencing disks
- RDP access with auto-generated passwords

```powershell
# Windows only - run as Administrator
cd examples\04-hyperv-local
.\setup-base-image.ps1    # First-time setup
boxy.exe serve --config boxy.yaml
```

See [04-hyperv-local/QUICKSTART.md](./04-hyperv-local/QUICKSTART.md) for step-by-step guide.

### 5. advanced-hooks.yaml

**Advanced features** - Demonstrates all hook capabilities.

Features shown:

- **Retry logic**: Retry failed hooks automatically
- **Continue on failure**: Optional hooks that don't fail allocation
- **Multiple shell types**: Bash and Python scripts
- **Template variables**: `${resource.id}`, `${username}`, `${password}`
- **Custom timeouts**: Per-hook and per-phase timeouts
- **Complex setup**: Multi-step provisioning and personalization

```bash
boxy serve --config examples/advanced-hooks.yaml
```

## Configuration Structure

### Basic Structure

```yaml
pools:
  - name: pool-name
    type: container|vm|process
    backend: docker|hyperv|kvm
    image: image-name
    min_ready: 3       # Keep this many ready
    max_total: 10      # Maximum total resources
    cpus: 2
    memory_mb: 2048

    hooks:
      after_provision: []   # Finalization hooks
      before_allocate: []   # Personalization hooks

    timeouts:
      provision: 5m
      finalization: 10m
      personalization: 30s
      destroy: 1m

storage:
  type: sqlite
  path: ~/.config/boxy/boxy.db

logging:
  level: info|debug|warn|error
  format: text|json
```

### Hook Configuration

```yaml
hooks:
  after_provision:
    - name: hook-name
      type: script
      shell: bash|powershell|python
      inline: |
        # Script content here
        echo "Resource: ${resource.id}"
      # OR
      path: /path/to/script.sh

      timeout: 5m              # Optional individual timeout
      retry: 3                 # Optional retry count
      continue_on_failure: true  # Optional: don't fail on error
```

### Template Variables

Available in hook scripts:

- `${resource.id}` - Resource UUID
- `${resource.ip}` - IP address (if available)
- `${resource.type}` - Resource type (vm, container, process)
- `${provider.id}` - Provider-specific ID
- `${pool.name}` - Pool name
- `${username}` - Allocated username (before_allocate only)
- `${password}` - Generated password (before_allocate only)
- `${metadata.key}` - Custom metadata values

## Hook Best Practices

### Finalization (after_provision)

**Purpose**: Prepare resources during pool warming

**Good for**:

- Installing packages
- Downloading large files
- System configuration
- Network validation
- Setting up monitoring

**Characteristics**:

- Slow operations (minutes)
- Background execution
- Happens before resource is ready
- Failure prevents resource from becoming ready

**Example**:

```yaml
after_provision:
  - name: install-packages
    shell: bash
    inline: |
      apt-get update
      apt-get install -y git vim curl
    timeout: 10m
    retry: 2
```

### Personalization (before_allocate)

**Purpose**: Customize resources for specific users

**Good for**:

- Creating user accounts
- Setting passwords
- Personalizing configuration
- Setting up user workspaces

**Characteristics**:

- Fast operations (seconds)
- Synchronous (user waits)
- Happens during allocation
- Failure releases resource back to pool

**Example**:

```yaml
before_allocate:
  - name: create-user
    shell: bash
    inline: |
      useradd -m ${username}
      echo "${username}:${password}" | chpasswd
    timeout: 30s
```

## Environment Variables

Override configuration with environment variables:

```bash
# Storage
export BOXY_STORAGE_TYPE=postgres
export BOXY_STORAGE_DSN="host=localhost user=boxy password=secret dbname=boxy"

# Logging
export BOXY_LOGGING_LEVEL=debug
export BOXY_LOGGING_FORMAT=json

# Start with env vars
boxy serve --config examples/simple-docker-pool.yaml
```

## Testing Configurations

Test configuration without starting the server:

```bash
# Validate configuration
boxy config validate examples/docker-with-hooks.yaml

# Show parsed configuration
boxy config show examples/docker-with-hooks.yaml
```

## Common Patterns

### 1. Development Environment

```yaml
pools:
  - name: dev-env
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 5
    max_total: 20
    hooks:
      after_provision:
        - name: install-tools
          shell: bash
          inline: apt-get update && apt-get install -y git vim curl
      before_allocate:
        - name: setup-user
          shell: bash
          inline: |
            useradd -m -s /bin/bash ${username}
            echo "${username}:${password}" | chpasswd
```

### 2. Build Farm

```yaml
pools:
  - name: build-agents
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 10
    max_total: 50
    cpus: 8
    memory_mb: 16384
    hooks:
      after_provision:
        - name: install-build-deps
          shell: bash
          inline: |
            apt-get update
            apt-get install -y gcc g++ make cmake ccache
          timeout: 15m
```

### 3. Testing Lab

```yaml
pools:
  - name: test-vms
    type: vm
    backend: hyperv
    image: Windows-Server-2022
    min_ready: 2
    max_total: 5
    hooks:
      after_provision:
        - name: install-test-tools
          shell: powershell
          inline: |
            Install-WindowsFeature -Name Web-Server
            Install-PackageProvider -Name NuGet -Force
```

## Troubleshooting

### Increase logging verbosity

```yaml
logging:
  level: debug
  format: json
```

### Check hook execution

Hooks store results in resource metadata:

```bash
# Get sandbox details
boxy sandbox get <sandbox-id>

# Look for:
# - finalization_hooks: Results from after_provision
# - personalization_hooks: Results from before_allocate
```

### Test hooks manually

```bash
# Get resource ID
boxy pool list

# Execute command in resource
boxy resource exec <resource-id> -- bash -c "echo 'test'"
```

## Next Steps

- Read [HOOKS.md](../docs/architecture/HOOKS.md) for detailed hook documentation
- Read [MVP_DESIGN.md](../docs/MVP_DESIGN.md) for architecture overview
- Try the examples!
- Create your own configuration
