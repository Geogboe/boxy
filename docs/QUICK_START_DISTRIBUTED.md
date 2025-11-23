# Quick Start: Distributed Boxy

This guide shows how to set up Boxy in distributed mode with a Linux server and Windows agent.

## Scenario

- **Server**: Linux host running Boxy server (manages Docker locally)
- **Agent**: Windows Server host running Boxy agent (provides Hyper-V)
- **Goal**: Provision both Docker containers and Hyper-V VMs from single Boxy instance

## Prerequisites

- Linux server (Ubuntu 20.04+) with Docker installed
- Windows Server (2019+) with Hyper-V enabled
- Network connectivity between hosts
- `boxy` binary installed on both hosts

## Step 1: Initialize Server

### On Linux Server (Generate Token)

```bash
# Initialize CA (one-time)
sudo boxy admin init-ca --output /etc/boxy/ca

# Create server config
sudo mkdir -p /etc/boxy
cat > /etc/boxy/boxy.yaml <<EOF
server:
  listen_address: 0.0.0.0:8443
  tls:
    cert_file: /etc/boxy/server-cert.pem
    key_file: /etc/boxy/server-key.pem
    ca_file: /etc/boxy/ca/ca-cert.pem
    client_auth: require

storage:
  type: sqlite
  path: /var/lib/boxy/boxy.db

pools:
  # Local Docker containers
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 5
    max_total: 20

  # Remote Hyper-V VMs (will be added after agent registration)
  # Will uncomment after agent is registered
  # - name: win-server-vms
  #   type: vm
  #   backend: hyperv
  #   backend_agent: windows-agent-01
  #   image: win-server-2022-template
  #   min_ready: 3
  #   max_total: 10
EOF

# Generate server certificate
sudo boxy admin issue-cert \
  --ca-cert /etc/boxy/ca/ca-cert.pem \
  --ca-key /etc/boxy/ca/ca-key.pem \
  --cert-type server \
  --common-name boxy-server \
  --dns-names boxy-server.internal,$(hostname) \
  --output /etc/boxy

# Start server
sudo boxy serve --config /etc/boxy/boxy.yaml
```

Server is now running on `https://<server-ip>:8443`

## Step 2: Register Windows Agent

### On Linux Server (Configure Pools)

```bash
# Generate registration token for Windows agent
boxy admin generate-agent \
  --providers hyperv \
  --max-resources 50 \
  --expires 24h

# Output:
# Token: reg_abc123xyz789def456ghi
# Expires: 2025-11-21 10:00:00 UTC

# Export CA certificate
boxy admin export-ca --output ca-cert.pem

# Copy ca-cert.pem to Windows host (use scp, USB, or secure file share)
scp ca-cert.pem administrator@windows-host:C:/Temp/
```

### On Windows Server

```powershell
# Install Boxy agent (one-time)
cd C:\Temp

boxy.exe agent install `
  --server https://boxy-server.internal:8443 `
  --ca ca-cert.pem `
  --token reg_abc123xyz789def456ghi

# Output:
# ✓ Connected to server: boxy-server.internal:8443
# ✓ Token validated
# ✓ Agent registered as: agent-abc123def456
# ✓ Certificate stored: C:\ProgramData\Boxy\Agent\cert.pem
# ✓ Agent installed as Windows service
# ✓ Agent started
#
# To check status: boxy agent status

# Verify agent is running
boxy.exe agent status

# View agent logs
Get-EventLog -LogName Application -Source BoxyAgent -Newest 10
```

## Step 3: Configure Pools for Remote Agent

### On Linux Server

Now that agent is registered, enable Hyper-V pool:

```bash
# Edit config to uncomment Hyper-V pool
sudo nano /etc/boxy/boxy.yaml

# Add agent configuration:
agents:
  - id: agent-abc123def456  # Use actual agent ID from registration
    address: windows-host.internal:8444
    providers:
      - hyperv
    max_resources: 50

# Uncomment Hyper-V pool and set backend_agent:
pools:
  - name: win-server-vms
    type: vm
    backend: hyperv
    backend_agent: agent-abc123def456  # Must match agent ID
    image: win-server-2022-template
    min_ready: 3
    max_total: 10
    cpus: 4
    memory_mb: 4096
    disk_gb: 60

# Restart server to load new config
sudo systemctl restart boxy
```

## Step 4: Verify Setup

### Check Agent Status

```bash
# On server
boxy admin list-agents

# Output:
# ID                  ADDRESS                      STATUS   PROVIDERS  RESOURCES
# agent-abc123def456  windows-host.internal:8444   healthy  hyperv     0/50
```

### Check Pools

```bash
boxy pool ls

# Output:
# NAME                TYPE       BACKEND  AGENT               READY  ALLOCATED  MIN  MAX
# ubuntu-containers   container  docker   (local)             5      0          5    20
# win-server-vms      vm         hyperv   agent-abc123def456  3      0          3    10
```

## Step 5: Create Sandboxes

### Docker Container Sandbox

```bash
# Create sandbox with Docker containers (local)
boxy sandbox create \
  --pool ubuntu-containers:2 \
  --duration 1h \
  --name dev-env

# Output:
# Sandbox created: sb-xyz789
# Resources:
#   - res-aaa111 (container): ubuntu:22.04
#   - res-bbb222 (container): ubuntu:22.04
# Expires: 2025-11-20 11:00:00
```

### Hyper-V VM Sandbox

```bash
# Create sandbox with Hyper-V VMs (remote agent)
boxy sandbox create \
  --pool win-server-vms:1 \
  --duration 2h \
  --name test-lab

# Output:
# Sandbox created: sb-abc456
# Resources:
#   - res-ccc333 (vm): Windows Server 2022
# Expires: 2025-11-20 12:00:00
```

### Mixed Sandbox

```bash
# Create sandbox with both Docker and Hyper-V
boxy sandbox create \
  --pool ubuntu-containers:2 \
  --pool win-server-vms:1 \
  --duration 4h \
  --name integration-test

# Output:
# Sandbox created: sb-mixed789
# Resources:
#   - res-ddd444 (container): ubuntu:22.04
#   - res-eee555 (container): ubuntu:22.04
#   - res-fff666 (vm): Windows Server 2022
# Expires: 2025-11-20 14:00:00
```

## Step 6: Interact with Resources

### Execute Commands in Container

```bash
# Get resource ID
resource_id=$(boxy sandbox get sb-xyz789 --json | jq -r '.resources[0].id')

# Execute command
boxy resource exec $resource_id -- apt-get update
boxy resource exec $resource_id -- apt-get install -y nginx
boxy resource exec $resource_id -- systemctl status nginx
```

### Execute Commands in VM (PowerShell Direct)

```bash
# Get VM resource ID
vm_id=$(boxy sandbox get sb-abc456 --json | jq -r '.resources[0].id')

# Execute PowerShell commands via PowerShell Direct
boxy resource exec $vm_id -- Get-Service
boxy resource exec $vm_id -- Install-WindowsFeature -Name Web-Server
boxy resource exec $vm_id -- Test-NetConnection -ComputerName google.com
```

## Monitoring

### View Agent Metrics

```bash
# Server metrics
curl http://localhost:9090/metrics | grep boxy_agent

# Example output:
# boxy_agent_status{agent_id="agent-abc123def456",status="healthy"} 1
# boxy_agent_resources_total{agent_id="agent-abc123def456",provider="hyperv"} 3
# boxy_agent_last_heartbeat_seconds{agent_id="agent-abc123def456"} 5
```

### View Logs

```bash
# Server logs (Linux)
journalctl -u boxy -f

# Agent logs (Windows)
Get-EventLog -LogName Application -Source BoxyAgent -Newest 50 | Format-Table -AutoSize
```

## Troubleshooting

### Agent Can't Connect

```powershell
# On Windows agent, test connection
boxy.exe agent test-connection

# Check network connectivity
Test-NetConnection boxy-server.internal -Port 8443

# Check certificate
openssl s_client -connect boxy-server.internal:8443 -CAfile C:\ProgramData\Boxy\Agent\ca.pem
```

### Pool Not Provisioning

```bash
# Check pool status
boxy pool stats win-server-vms

# Check agent status
boxy admin list-agents

# View detailed logs
boxy serve --log-level debug
```

### Certificate Expired

```bash
# Check certificate expiration
openssl x509 -in /etc/boxy/ca/ca-cert.pem -noout -dates

# Generate new registration token
boxy admin generate-agent --providers hyperv --expires 24h

# On agent, uninstall and reinstall with new token
boxy agent uninstall
boxy agent install --server https://boxy-server:8443 --ca ca-cert.pem --token <new-token>
```

## Security Recommendations

1. **Firewall Rules**:

   ```bash
   # On server: Allow agent connections
   sudo ufw allow from 10.0.1.0/24 to any port 8443 proto tcp

   # On agent: Allow server connections
   New-NetFirewallRule -DisplayName "Boxy Agent" -Direction Inbound -LocalPort 8444 -Protocol TCP -Action Allow
   ```

2. **Certificate Rotation**:
  - Set calendar reminder to rotate agent certificates every 60 days
  - Automate renewal with cron/scheduled task

3. **Monitoring**:
  - Set up alerts for agent disconnections
  - Monitor certificate expiration (alert 30 days before)
  - Track resource usage per agent

4. **Access Control**:
  - Restrict who can generate registration tokens
  - Audit all agent registrations
  - Revoke unused tokens

## Next Steps

1. **Add More Agents**: Repeat Step 2 for additional agent hosts
2. **Configure Auto-Scaling**: Adjust min_ready/max_total based on demand
3. **Set Up Monitoring**: Configure Prometheus + Grafana for metrics
4. **Automate Deployment**: Create Terraform/Ansible playbooks for setup
5. **Enable API**: Access Boxy programmatically via REST API

## Summary

You now have:

- ✅ Boxy server managing local Docker provider
- ✅ Boxy agent exposing remote Hyper-V provider
- ✅ Secure mTLS communication between server and agent
- ✅ Ability to provision mixed sandboxes (Docker + Hyper-V)
- ✅ Resource interaction via Execute() (docker exec, PowerShell Direct)

The distributed architecture is transparent - from the pool manager's perspective, remote providers look identical to local ones!
