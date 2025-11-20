# Hook-Based Architecture

## Overview

Boxy uses **hooks** to allow customization at specific lifecycle points. Hooks are shell scripts that run during resource provisioning and allocation.

## Two Lifecycle Phases

### Phase 1: Finalization (Pool Warming)
Background process that prepares resources for the pool:
```
Base Image → Provider.Provision() → Hooks (after_provision) → Pool (ready)
```
- Can be slow (minutes)
- User-provided scripts allowed
- Validates resource is functional

### Phase 2: Personalization (Allocation)
Fast operations when user requests a resource:
```
Pool Resource → Hooks (before_allocate) → User
```
- Must be fast (seconds)
- Makes resource unique (user account, hostname)
- User is waiting

## Hook Points

```yaml
hooks:
  # Phase 1: Finalization (pool warming)
  after_provision:
    - name: validate-network
      type: script
      shell: bash
      inline: ping -c 1 8.8.8.8
      timeout: 30s

    - name: custom-setup
      type: script
      shell: powershell
      path: ./setup.ps1
      timeout: 300s

  # Phase 2: Personalization (allocation)
  before_allocate:
    - name: create-user
      type: script
      shell: powershell
      inline: |
        New-LocalUser -Name "${username}" -Password (ConvertTo-SecureString "${password}" -AsPlainText -Force)
      timeout: 10s
```

## Hook Configuration

### Pool-Level Hooks

```yaml
pools:
  - name: ubuntu-containers
    image: ubuntu:22.04

    hooks:
      after_provision:
        - type: script
          shell: bash
          inline: |
            apt-get update
            apt-get install -y nginx
          timeout: 120s

      before_allocate:
        - type: script
          shell: bash
          inline: |
            useradd -m -s /bin/bash ${username}
            echo "${username}:${password}" | chpasswd
          timeout: 5s
```

### System Hooks (Boxy Built-in)

Boxy provides default hooks that run automatically unless disabled:

**Linux (after_provision)**:
- Wait for network connectivity
- Basic package manager update
- Install ca-certificates, curl, wget

**Windows (after_provision)**:
- Wait for network connectivity
- Ensure WinRM running
- Set timezone to UTC

**All (before_allocate)**:
- Create user account with random password
- Set unique hostname

**Disable system hooks**:
```yaml
pools:
  - name: my-pool
    use_system_hooks: false  # No Boxy defaults
    hooks:
      after_provision:
        - type: script
          # ... your complete setup
```

## Hook Types

### Script Hook

Run shell commands:

```yaml
- type: script
  shell: bash | powershell | python
  inline: |
    # Script content here
  # OR
  path: ./script.sh

  # Optional properties
  timeout: 30s
  retry: 3
  continue_on_failure: false
```

**Supported shells**:
- `bash` - Linux shell scripts
- `powershell` - Windows PowerShell
- `python` - Python 3 scripts

### Hook Execution

Hooks run via Provider.Execute():

```go
// Pool manager runs hooks
for _, hook := range pool.Hooks.AfterProvision {
    cmd := buildCommand(hook) // e.g., ["bash", "-c", "script content"]

    result, err := provider.Execute(ctx, resource, cmd)
    if err != nil || result.ExitCode != 0 {
        if !hook.ContinueOnFailure {
            return fmt.Errorf("hook %s failed: %s", hook.Name, result.Stderr)
        }
    }
}
```

## Hook Variables

Hooks can access resource metadata via template variables:

```yaml
hooks:
  before_allocate:
    - type: script
      shell: bash
      inline: |
        # Available variables:
        # ${resource.id}     - Resource UUID
        # ${resource.ip}     - IP address (if available)
        # ${username}        - Allocated username
        # ${password}        - Generated password
        # ${pool.name}       - Pool name

        echo "Setting up ${username} on ${resource.id}"
        useradd ${username}
        echo "${username}:${password}" | chpasswd
```

## Async Allocation

Allocations with hooks may take time. CLI waits automatically:

```bash
$ boxy sandbox create -p win-server-vms:1 -d 8h

Provisioning... ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 30s
Running hooks... ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 45s

✓ Sandbox ready: sb-xyz789

Connection Info:
  RDP: 10.0.1.50:3389
  Username: Administrator
  Password: x7Ks9mP2nQ
```

**API returns immediately**:
```json
POST /api/v1/sandboxes
{
  "pool": "win-server-vms:1",
  "duration": "8h"
}

Response (202 Accepted):
{
  "sandbox_id": "sb-xyz789",
  "status": "provisioning",
  "estimated_time": "60s"
}

GET /api/v1/sandboxes/sb-xyz789
{
  "sandbox_id": "sb-xyz789",
  "status": "ready",
  "resources": [...]
}
```

## Timeouts

Configure timeouts at multiple levels:

```yaml
pools:
  - name: my-pool

    # Phase-level timeouts
    timeouts:
      provision: 300s       # Provider.Provision() max time
      finalization: 600s    # All after_provision hooks combined
      personalization: 30s  # All before_allocate hooks combined
      destroy: 60s          # Provider.Destroy() max time

    hooks:
      after_provision:
        # Individual hook timeout
        - type: script
          timeout: 120s
```

**Hierarchy**: Individual hook timeout < Phase timeout

If any timeout is exceeded, the operation fails and resource is destroyed.

## Debugging Hooks

### Verbose Logging

```bash
# Run with debug logging
boxy serve --log-level debug

# Shows:
# - Each hook execution
# - Command being run
# - stdout/stderr output
# - Exit codes
# - Timing
```

### Manual Hook Testing

```bash
# Test finalization hooks on a pool
boxy admin pool test-finalization win-server-vms --debug

# Test personalization on a specific resource
boxy admin resource test-personalization res-abc123 \
  --username testuser \
  --password testpass \
  --debug
```

### Hook Failure Handling

```yaml
hooks:
  after_provision:
    # Critical hook - fail if this fails
    - type: script
      shell: bash
      inline: curl https://license-server/activate
      timeout: 30s
      retry: 3

    # Non-critical - continue even if fails
    - type: script
      shell: bash
      inline: apt-get install -y optional-package
      timeout: 60s
      continue_on_failure: true
```

## Examples

### Ubuntu Container with Nginx

```yaml
pools:
  - name: ubuntu-nginx
    image: ubuntu:22.04

    hooks:
      after_provision:
        - type: script
          shell: bash
          inline: |
            apt-get update
            apt-get install -y nginx
            systemctl enable nginx
          timeout: 120s

      before_allocate:
        - type: script
          shell: bash
          inline: |
            # Create user account
            useradd -m -s /bin/bash ${username}
            echo "${username}:${password}" | chpasswd

            # Add to sudo group
            usermod -aG sudo ${username}
          timeout: 10s
```

### Windows Server (Stub Example)

```yaml
pools:
  - name: win-server-vms
    image: win-server-2022-base
    backend: hyperv

    hooks:
      after_provision:
        - type: script
          shell: powershell
          inline: |
            # Validate network
            while (!(Test-NetConnection -ComputerName 8.8.8.8 -InformationLevel Quiet)) {
                Start-Sleep -Seconds 5
            }

            # Install features
            Install-WindowsFeature Web-Server
          timeout: 300s

      before_allocate:
        - type: script
          shell: powershell
          inline: |
            # Create local user
            $SecurePassword = ConvertTo-SecureString "${password}" -AsPlainText -Force
            New-LocalUser -Name "${username}" -Password $SecurePassword
            Add-LocalGroupMember -Group "Administrators" -Member "${username}"
          timeout: 10s
```

## Future Enhancements

**Phase 2+**:
- Built-in hook types: `domain-join`, `ssh-key`, `ansible`
- Conditional hooks: `condition: ${resource.os} == 'windows'`
- Hook dependencies: `depends_on: [other-hook]`
- Parallel hook execution: `parallel: true`
- Hook outputs: Access output from previous hooks

## Summary

**Hooks provide**:
- ✅ Flexibility: Run any script at any lifecycle point
- ✅ Simplicity: Just shell scripts, no complex abstractions
- ✅ Layering: System defaults + pool-specific + allocation-specific
- ✅ Debugging: Test hooks independently, verbose logging
- ✅ Extensibility: Add new hook points as needed

**MVP Implementation**:
- Two hook points: `after_provision`, `before_allocate`
- Script hook type only (bash, powershell, python)
- Timeout and retry support
- Template variables for resource metadata
- Auto-wait in CLI, async in API
