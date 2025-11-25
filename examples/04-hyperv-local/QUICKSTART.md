# Hyper-V Quickstart

Quick reference for running Boxy with local Hyper-V.

## Prerequisites Checklist

- [ ] Windows 10/11 Pro/Enterprise or Windows Server 2016+
- [ ] Hyper-V installed and running (`Get-Service vmms` shows Running)
- [ ] Running PowerShell as **Administrator**
- [ ] Base VHD image created (see README.md or run `setup-base-image.ps1`)

## Quick Commands

### 1. First-Time Setup

```powershell
# Run setup script to create directories and check Hyper-V
.\setup-base-image.ps1

# If you have a Windows ISO:
.\setup-base-image.ps1 -ISOPath "C:\ISOs\WindowsServer2022.iso"
```

### 2. Start Boxy

```powershell
# Build Boxy (from repository root)
go build -o boxy.exe ./cmd/boxy

# Start server (must run as Administrator)
.\boxy.exe serve --config examples\04-hyperv-local\boxy.yaml
```

**Expected output:**
```
INFO Hyper-V provider registered
INFO Pool manager started                        pool=windows-vm-pool
INFO Sandbox manager started
```

### 3. Create Your First Sandbox

```powershell
# In a new PowerShell window (as Administrator)
.\boxy.exe sandbox create `
    --config examples\04-hyperv-local\boxy.yaml `
    -p windows-vm-pool:1 `
    -d 2h `
    --name my-sandbox
```

**Output shows:**
- Sandbox ID
- VM IP address
- Username (Administrator)
- Password (auto-generated, secure)

### 4. Connect to the VM

```powershell
# Use Remote Desktop (replace with your VM's IP from output)
mstsc /v:192.168.1.123

# Or create RDP file:
@"
full address:s:192.168.1.123:3389
username:s:Administrator
"@ | Out-File my-sandbox.rdp

mstsc my-sandbox.rdp
```

Enter the password shown in the sandbox creation output.

### 5. List Sandboxes

```powershell
.\boxy.exe sandbox list --config examples\04-hyperv-local\boxy.yaml
```

### 6. Check Pool Status

```powershell
.\boxy.exe pool list --config examples\04-hyperv-local\boxy.yaml
```

Shows:
- How many VMs are ready
- How many are allocated
- Pool health

### 7. Destroy Sandbox

```powershell
# Replace with your sandbox ID
.\boxy.exe sandbox destroy abc12345 --config examples\04-hyperv-local\boxy.yaml
```

The VM is stopped, removed, and the differencing disk deleted. The base image remains for future use.

## Understanding Preheating

With `min_ready: 1` in the config:

1. **Server starts** → Provisions 1 VM immediately (~30 seconds)
2. **You create sandbox** → Instant (uses preheated VM)
3. **Pool replenishes** → Provisions another VM in background
4. **You create another** → Instant again!

This means after the initial warmup, sandbox creation is **instant** (VM already running).

## Typical Workflow

```powershell
# Terminal 1: Start Boxy (leave running)
.\boxy.exe serve --config examples\04-hyperv-local\boxy.yaml

# Terminal 2: Create sandbox when needed
.\boxy.exe sandbox create -p windows-vm-pool:1 -d 2h --config examples\04-hyperv-local\boxy.yaml

# Connect via RDP
# ... do your work ...

# Destroy when done
.\boxy.exe sandbox destroy <id> --config examples\04-hyperv-local\boxy.yaml
```

## Troubleshooting

**"Access denied"** → Run PowerShell as Administrator

**"Provider not found"** → Hyper-V not installed or service not running

**"Base image not found"** → Run `setup-base-image.ps1` or create base VHD manually

**"Timeout waiting for IP"** → VM is still booting or DHCP issue (VM still usable, just no IP yet)

## Configuration Tweaks

### More VMs (if you have resources)

```yaml
min_ready: 3      # Keep 3 VMs warm
max_total: 10     # Allow up to 10 total
```

### Smaller VMs (to fit more)

```yaml
cpus: 1
memory_mb: 2048   # 2GB instead of 4GB
```

### Longer sandbox lifetime

```bash
-d 8h             # 8 hour sandboxes instead of 2h
```

## Next Steps

- Add hooks to customize VMs (install software automatically)
- Configure multiple pools with different base images
- Enable HTTP API for programmatic access
- See full documentation in README.md
