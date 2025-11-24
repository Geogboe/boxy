# Quickstart: Scratch Provider

**The fastest way to try Boxy!** No Docker or VMs required.

The scratch/shell provider creates lightweight filesystem-based workspaces - perfect for learning Boxy, local development, and testing.

## What You'll Learn

- Start Boxy server
- Create a sandbox
- Access your workspace
- Destroy resources

## Prerequisites

- Boxy installed (`go install` or build from source)
- A Unix-like system (Linux, macOS, WSL)
- 5 minutes

## Quick Start

### 1. Start Boxy

```bash
# From the boxy repository root
boxy serve --config examples/00-quickstart-scratch/boxy.yaml
```

You should see:
```
✓ Boxy service started successfully
  • 1 pools active
  • Database: ~/.config/boxy/quickstart.db

Press Ctrl+C to stop
```

### 2. Create a Sandbox

In another terminal:

```bash
# Create a sandbox with one workspace
boxy sandbox create --pool scratch-pool:1 --duration 1h --name my-workspace
```

Output:
```
Creating sandbox...
✓ Sandbox abc123de created (allocating resources...)
Waiting for resources ✓

✓ Sandbox ready

ID:         abc123de-f456-7890-abcd-ef1234567890
Name:       my-workspace
Resources:  1
State:      ready
Expires:    2024-01-15T14:30:00Z (in 59m59s)

Resource Connection Info:
─────────────────────────

[1] Resource abc123de
    Type: shell
    Workspace dir: /tmp/boxy-scratch/abc123de-f456-7890-abcd-ef1234567890/workspace
    Connect script: /tmp/boxy-scratch/abc123de-f456-7890-abcd-ef1234567890/connect.sh
```

### 3. Access Your Workspace

Use the provided connect script:

```bash
# Execute the connect script
/tmp/boxy-scratch/abc123de-f456-7890-abcd-ef1234567890/connect.sh
```

This launches a shell in your workspace:
```
Welcome to your Boxy workspace!
Workspace: /tmp/boxy-scratch/abc123de-f456-7890-abcd-ef1234567890/workspace
[boxy:abc123de] ~/workspace$
```

Or access the workspace directory directly:

```bash
cd /tmp/boxy-scratch/abc123de-f456-7890-abcd-ef1234567890/workspace
ls -la
```

You'll see:
```
.bashrc       # Custom shell configuration
.boxy-env     # Environment variables
README.md     # Workspace info
```

### 4. Use Your Workspace

```bash
# Create files
echo "Hello from Boxy!" > hello.txt

# Run commands
git clone https://github.com/yourrepo/project.git
cd project
make build

# Your workspace is just a directory - use it however you want!
```

### 5. List Sandboxes

```bash
boxy sandbox list
```

### 6. Clean Up

```bash
# Destroy the sandbox
boxy sandbox destroy abc123de-f456-7890-abcd-ef1234567890
```

The workspace directory is automatically removed.

## What Just Happened?

1. **Boxy started** and created a pool called `scratch-pool`
2. **3 workspaces were pre-provisioned** (`min_ready: 3`)
3. **You created a sandbox** which allocated one workspace from the pool
4. **Hooks ran** to customize your workspace:
   - `after_provision`: Created README during pool warming
   - `before_allocate`: Personalized `.bashrc` when allocated
5. **You got connection info** pointing to your workspace directory

## Understanding the Structure

Each workspace has this layout:

```
/tmp/boxy-scratch/
└── {resource-id}/
    ├── workspace/          # Your working directory
    │   ├── .bashrc        # Custom shell config
    │   ├── .boxy-env      # Environment variables
    │   └── README.md      # Workspace info
    ├── connect.sh         # Script to enter workspace
    └── .boxy-resource     # Internal metadata
```

## Configuration Options

### Change Base Directory

```yaml
extra_config:
  base_dir: ~/my-boxy-workspaces  # Custom location
```

### Configure Shells

```yaml
extra_config:
  allowed_shells:
    - bash
    - zsh
    - sh
```

Boxy tries each shell in order until it finds one that exists.

### Require Free Space

```yaml
extra_config:
  min_free_bytes: 5368709120  # Require 5GB free
```

Health checks will fail if free space drops below this threshold.

## Common Use Cases

### 1. Quick Testing

```bash
# Create workspace
boxy sandbox create --pool scratch-pool:1 --duration 15m

# Test something
cd /tmp/boxy-scratch/.../workspace
./run-tests.sh

# Auto-cleanup after 15 minutes
```

### 2. Isolated Builds

```bash
# Each build gets a clean workspace
boxy sandbox create --pool scratch-pool:1 --duration 1h --name "build-${CI_JOB_ID}"

# Build in isolation
cd /tmp/boxy-scratch/.../workspace
git clone $REPO
make build

# Destroy when done
boxy sandbox destroy $SANDBOX_ID
```

### 3. Development Environments

```yaml
# Add dev tools in hooks
after_provision:
  - name: install-tools
    shell: bash
    inline: |
      # Your workspace setup here
      ln -s ~/projects /tmp/boxy-scratch/${resource.id}/workspace/projects
```

## Limitations

⚠️ **No Isolation**: The scratch provider does NOT provide:
- Process isolation
- Network isolation
- User isolation
- Security boundaries

For production workloads or untrusted code, use Docker or Hyper-V providers instead.

## Next Steps

- Try [01-simple-docker-pool](../01-simple-docker-pool/) for real isolation
- Learn about [hooks](../02-hooks-demo/) for advanced customization
- Read [remote agents](../03-remote-agent/) for distributed resources

## Troubleshooting

### "No allowed shell found"

Install bash or sh:
```bash
# Ubuntu/Debian
sudo apt-get install bash

# macOS (bash is pre-installed)
```

### Permission Denied

Check base directory permissions:
```bash
chmod 755 /tmp/boxy-scratch
```

### Workspaces Not Created

Check logs:
```yaml
logging:
  level: debug  # More verbose
```

Then restart Boxy.
