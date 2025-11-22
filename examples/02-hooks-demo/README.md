# Example 2: Hook-Based Provisioning

This example demonstrates Boxy's powerful hook system for two-phase provisioning:
- **Finalization** (after_provision): Run during pool warming
- **Personalization** (before_allocate): Run during allocation

## What This Demonstrates

- Hook execution inside containers
- Template variable expansion (`${username}`, `${password}`, etc.)
- Two-phase provisioning (finalization + personalization)
- Timeout and retry handling
- Hook results stored in metadata

## Prerequisites

- Docker installed and running
- Boxy binary built

## Files

- `boxy.yaml` - Configuration with hooks
- `run.sh` - Start the Boxy service
- `test.sh` - Create sandbox and inspect hooks
- `verify-hooks.sh` - Connect to container and verify hook results

## Quick Start

```bash
# 1. Start Boxy service
./run.sh

# 2. In another terminal, create sandbox
./test.sh

# 3. Verify hooks ran successfully
./verify-hooks.sh <sandbox-id>
```

## Hook Flow

### Phase 1: Finalization (During Pool Warming)

Runs when container is first provisioned (background, slow operations):

```yaml
after_provision:
  - name: install-tools
    shell: bash
    inline: |
      apt-get update -qq
      apt-get install -y -qq curl vim htop
      echo "Tools installed at $(date)" > /provisioned.txt
    timeout: 5m
```

**Result**: Container has tools pre-installed, ready for allocation.

### Phase 2: Personalization (During Allocation)

Runs when sandbox is created (fast, user-specific):

```yaml
before_allocate:
  - name: create-user
    shell: bash
    inline: |
      useradd -m ${username}
      echo "${username}:${password}" | chpasswd
      echo "User ${username} created" > /home/${username}/welcome.txt
    timeout: 30s
```

**Result**: Unique user created with credentials from template variables.

## Template Variables

Available during hook execution:

- `${resource.id}` - Resource UUID
- `${resource.ip}` - Container IP address
- `${username}` - Generated username
- `${password}` - Generated password
- `${pool.name}` - Pool name

## Expected Output

```
# After provisioning (finalization phase)
Container provisioned: container-abc123
Finalization hooks executed:
  ✓ install-tools (2.5s)
Container state: Ready

# After allocation (personalization phase)
Sandbox allocated: sb-xyz789
Personalization hooks executed:
  ✓ create-user (0.3s)
Connection: ssh user@container-ip (password: generated)
```

## Hook Verification

```bash
# After creating sandbox, verify hooks ran
docker exec <container-id> cat /provisioned.txt
# Output: Tools installed at Mon Nov 21 12:00:00 UTC 2025

docker exec <container-id> cat /home/boxy-user/welcome.txt
# Output: User boxy-user created

docker exec <container-id> ls /usr/bin/vim
# Output: /usr/bin/vim
```

## Configuration Explained

```yaml
hooks:
  after_provision:  # Finalization: slow, run once
    - name: install-tools
      type: script
      shell: bash
      inline: |
        apt-get update
        apt-get install -y tools
      timeout: 5m
      retry: 2

  before_allocate:  # Personalization: fast, per-user
    - name: create-user
      type: script
      shell: bash
      inline: |
        useradd -m ${username}
        echo "${username}:${password}" | chpasswd
      timeout: 30s

timeouts:
  provision: 5m
  finalization: 10m      # Total time for all after_provision hooks
  personalization: 2m    # Total time for all before_allocate hooks
```

## Why Two Phases?

**Without hooks**: Every allocation waits for slow setup (5+ minutes)

**With two-phase hooks**:
1. Finalization (5 min) - Happens in background during pool warming
2. Personalization (30 sec) - Happens during allocation

**Result**: User gets customized environment in 30 seconds, not 5 minutes!

## Next Steps

- Try `examples/03-remote-agent` to see distributed architecture
- Modify hooks to install your own tools
- Add continue-on-failure for non-critical hooks
