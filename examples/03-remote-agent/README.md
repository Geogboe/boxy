# Example 3: Remote Agent Architecture

## What This Demonstrates

This example shows Boxy's **distributed agent architecture**, which enables:

- **Cross-platform orchestration**: Run Boxy server on Linux while managing Windows VMs
- **Remote resource management**: Agents run on machines with local resources (Hyper-V, VMware, etc.)
- **Centralized control**: Single Boxy server coordinates multiple remote agents

## Architecture Overview

```text
┌─────────────────────────────────────────────────────────┐
│                   Linux Machine                          │
│                                                          │
│  ┌────────────────────────────────────────────────────┐ │
│  │          Boxy Server (boxy serve)                  │ │
│  │  - Manages pools and sandboxes                     │ │
│  │  - Coordinates resource requests                   │ │
│  │  - Stores state in database                        │ │
│  └────────────────────────────────────────────────────┘ │
│                       │                                  │
│                       │ gRPC/HTTP2                       │
│                       ▼                                  │
└─────────────────────────────────────────────────────────┘
                        │
                        │ Network (LAN/VPN)
                        │
┌─────────────────────────────────────────────────────────┐
│                  Windows Machine                         │
│                                                          │
│  ┌────────────────────────────────────────────────────┐ │
│  │        Boxy Agent (boxy agent serve)               │ │
│  │  - Exposes local Hyper-V provider                  │ │
│  │  - Executes provision/destroy commands             │ │
│  │  - Reports resource status                         │ │
│  └────────────────────────────────────────────────────┘ │
│                       │                                  │
│                       ▼                                  │
│            ┌─────────────────────┐                       │
│            │   Hyper-V Service   │                       │
│            │   (Local VMs)       │                       │
│            └─────────────────────┘                       │
└─────────────────────────────────────────────────────────┘
```

## Use Case

**Problem**: You want to manage Windows VMs with Hyper-V, but you're running Boxy on a Linux machine.

**Solution**:

1. Run `boxy agent serve` on your Windows machine (exposes Hyper-V locally)
2. Run `boxy serve` on your Linux machine (coordinates everything)
3. Server sends gRPC requests to agent when provisioning VMs
4. Agent executes commands on local Hyper-V and returns results

## Files in This Example

- **agent-config.yaml** - Configuration for the agent (Windows machine)
- **server-config.yaml** - Configuration for the server (Linux machine)
- **start-agent.sh** (or start-agent.bat) - Start the agent
- **start-server.sh** - Start the server
- **test.sh** - Test remote provisioning

## Setup Instructions

### Step 1: Start the Agent (Windows Machine)

On your Windows machine with Hyper-V:

```bash
# Option A: Using bash (Git Bash, WSL)
./start-agent.sh

# Option B: Using PowerShell/CMD
.\start-agent.bat

# Option C: Manual
boxy agent serve --config agent-config.yaml
```

The agent will:

- Listen on port 50051 (default)
- Auto-detect and enable Hyper-V provider
- Wait for connections from the server

Expected output:

```text
INFO[0000] Starting Boxy Agent                           arch=amd64 os=windows version=vdev
INFO[0000] Auto-detected Windows platform, enabling Hyper-V provider
INFO[0000] Registered Hyper-V provider
INFO[0000] Agent server started successfully            address=:50051 agent_id=DESKTOP-ABC-1234567890 tls=false
```

### Step 2: Start the Server (Linux Machine)

On your Linux machine:

```bash
./start-server.sh
```

The server will:

- Connect to the remote agent at `windows-host:50051`
- Register the remote Hyper-V provider
- Create pools using the remote provider
- Start accepting requests

Expected output:

```text
INFO[0000] Starting Boxy Service
INFO[0000] Registering remote provider                   agent=windows-agent name=windows-agent-hyperv provider=hyperv
INFO[0000] Connected to remote agent                     address=windows-host:50051
INFO[0000] Starting pool manager
INFO[0000] Warming pool...                              pool=hyperv-pool
INFO[0000] Boxy service started                         address=:8080
```

### Step 3: Test Remote Provisioning

```bash
./test.sh
```

This will:

1. Check pool status (should show warming Hyper-V VMs)
2. Create a sandbox (triggers remote VM creation)
3. Get connection info (RDP details for the VM)
4. Destroy the sandbox

## Configuration Explained

### Agent Configuration (agent-config.yaml)

```yaml
# Agent runs on Windows machine
# Exposes local Hyper-V provider via gRPC

# No pools defined - agent only provides access to local resources
# No storage needed - agent is stateless

logging:
  level: info
  format: text
```

Agents are **stateless** - they don't manage pools or sandboxes, they just execute commands from the server.

### Server Configuration (server-config.yaml)

```yaml
# Server runs on Linux machine
# Coordinates everything and stores state

agents:
  - id: windows-agent
    address: "windows-host:50051"  # Change to your Windows machine IP/hostname
    providers: ["hyperv"]
    use_tls: false  # Set to true in production

pools:
  - name: hyperv-pool
    type: vm
    backend: windows-agent-hyperv  # Points to remote agent
    image: "windows-server-2022"
    min_ready: 1
    max_total: 3
    cpus: 2
    memory_mb: 4096
    disk_gb: 60

storage:
  type: sqlite
  path: ./boxy.db

logging:
  level: info
  format: text
```

Key points:

- `agents[]` defines remote agents to connect to
- `pools[].backend` uses format `{agent-id}-{provider-name}`
- Server stores all state (agents are stateless)

## Connection Security

This example uses **insecure mode** (`use_tls: false`) for simplicity. This is fine for:

- Testing/development
- Trusted internal networks (LAN)
- Lab environments

For production, enable TLS:

```yaml
agents:
  - id: windows-agent
    address: "windows-host:50051"
    providers: ["hyperv"]
    use_tls: true
    tls_cert_path: "/path/to/client.crt"
    tls_key_path: "/path/to/client.key"
    tls_ca_path: "/path/to/ca.crt"
```

See the Security Guide for certificate setup.

## Troubleshooting

### Agent won't start

**Error**: `Failed to create agent server: listen tcp :50051: bind: address already in use`

**Solution**: Another process is using port 50051. Either:

- Stop the other process
- Change the port: `boxy agent serve --listen :50052`

---

**Error**: `Failed to register providers: unsupported platform: windows`

**Solution**: Hyper-V provider not available. Check:

- Are you on Windows? (`echo $OSENV` should show Windows)
- Is Hyper-V installed? (`Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V`)
- Use `--providers mock` for testing without Hyper-V

### Server can't connect to agent

**Error**: `Failed to connect to remote agent: connection refused`

**Solution**:

- Check agent is running: `ps aux | grep "boxy agent"`
- Verify firewall allows port 50051
- Ping the Windows machine: `ping windows-host`
- Try IP address instead of hostname in server config

---

**Error**: `context deadline exceeded` when connecting

**Solution**:

- Network latency too high
- Agent may be under heavy load
- Increase timeout in server config (future enhancement)

### Remote provisioning fails

**Error**: `Provision failed: Hyper-V service not running`

**Solution**: On Windows machine:

```powershell
Get-Service vmms  # Check Hyper-V Virtual Machine Management service
Start-Service vmms  # Start if stopped
```

---

**Error**: `Provision failed: insufficient memory`

**Solution**: Reduce VM memory in pool config:

```yaml
memory_mb: 2048  # Instead of 4096
```

## What Happens Behind the Scenes

When you create a sandbox with a remote pool:

1. **Server receives request**: `boxy sandbox create --pool hyperv-pool:1`
2. **Server finds pool**: `hyperv-pool` uses backend `windows-agent-hyperv`
3. **Server resolves backend**: Backend is a RemoteProvider pointing to agent
4. **Server sends gRPC**: `ProviderService.Provision(spec)` → Windows agent
5. **Agent receives request**: Extracts provider name (`hyperv`)
6. **Agent finds local provider**: Looks up local Hyper-V provider instance
7. **Agent calls provider**: `hypervProvider.Provision(spec)`
8. **Hyper-V creates VM**: VM spins up on Windows machine
9. **Agent returns response**: `ProvisionResponse{resource}` → Server
10. **Server stores resource**: Updates database with resource details
11. **Server returns to user**: Sandbox created with connection info

All communication uses:

- **Protocol Buffers** for efficient serialization
- **gRPC** for RPC transport
- **HTTP/2** for connection multiplexing
- **Keepalive** for connection health (10s ping)

## Testing Without Hyper-V

If you don't have Hyper-V available, you can test the agent architecture using the **mock provider**:

**On agent machine**:

```bash
boxy agent serve --providers mock --listen :50051
```

**In server-config.yaml**:

```yaml
agents:
  - id: test-agent
    address: "localhost:50051"
    providers: ["mock"]

pools:
  - name: mock-pool
    type: vm
    backend: test-agent-mock  # Uses mock provider
    image: "mock-vm"
    min_ready: 2
    max_total: 5
```

The mock provider simulates realistic provisioning delays and responses without requiring real virtualization.

## Next Steps

After understanding the agent architecture, see:

- **Example 4: Complete Lab Environment** - Multi-pool, multi-agent setup
- **Security Guide** - Setting up mTLS for production
- **Deployment Guide** - Running agents as services

## Key Takeaways

✅ **Agents enable cross-platform orchestration**

- Run server on Linux, manage Windows VMs seamlessly

✅ **Agents are stateless**

- Server stores all state
- Agents just execute commands
- Easy to add/remove agents

✅ **Communication is efficient**

- Long-running HTTP/2 connections
- Connection multiplexing
- Built-in keepalive

✅ **Security is configurable**

- Insecure mode for testing
- mTLS for production
- No credentials over network
