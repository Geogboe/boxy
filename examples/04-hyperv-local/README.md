# Hyper-V Local Example

This example demonstrates running Boxy on a local Windows machine using Hyper-V with the embedded agent mode. This is perfect for Windows development environments where you have Hyper-V available locally.

## What This Example Shows

- **Embedded agent mode**: All providers run in the same process as the server
- **Hyper-V provider**: Provision Windows VMs using local Hyper-V
- **Preheating**: Keep 1 VM ready at all times for instant allocation
- **Simple configuration**: Minimal setup for local development

## Prerequisites

### System Requirements

1. **Windows 10/11 Pro/Enterprise** or **Windows Server 2016+**
2. **Hyper-V enabled** (see below for installation)
3. **Administrator privileges** (required for Hyper-V operations)
4. **At least 16GB RAM** (8GB for host, 4GB for VM, 4GB buffer)
5. **SSD storage recommended** (for faster VM provisioning)

### Install Hyper-V

**Windows 10/11 Pro/Enterprise:**

```powershell
# Run in PowerShell as Administrator
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All
```

After installation, **reboot your computer**.

**Verify Hyper-V is running:**

```powershell
# Check Hyper-V service
Get-Service vmms

# Should show: Status = Running

# Check Hyper-V host info
Get-VMHost
```

### Create a Base VHD Image

Boxy uses **differencing disks** to quickly provision VMs from a base image. You need to create at least one base VHD:

#### Option 1: Quick Test Image (Using Existing VM)

If you already have a Windows VM in Hyper-V:

```powershell
# 1. Stop the VM
Stop-VM -Name "YourExistingVM"

# 2. Export the VHD
Copy-Item "C:\Path\To\YourVM.vhdx" `
    -Destination "C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx"
```

#### Option 2: Create Fresh Base Image

**Step 1: Create directories:**

```powershell
New-Item -ItemType Directory -Path "C:\ProgramData\Boxy\BaseImages" -Force
New-Item -ItemType Directory -Path "C:\ProgramData\Boxy\VMs" -Force
New-Item -ItemType Directory -Path "C:\ProgramData\Boxy\VHDs" -Force
```

**Step 2: Create base VHD:**

```powershell
# Create a 60GB dynamic VHD
New-VHD -Path "C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx" `
    -SizeBytes 60GB -Dynamic

# Create temporary VM for installation
New-VM -Name "BaseImage-Setup" `
    -MemoryStartupBytes 4GB `
    -Generation 2 `
    -VHDPath "C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx"

# Add Windows ISO for installation
Add-VMDvdDrive -VMName "BaseImage-Setup" `
    -Path "C:\Path\To\WindowsServer2022.iso"

# Connect to a switch (use Default Switch)
Connect-VMNetworkAdapter -VMName "BaseImage-Setup" -SwitchName "Default Switch"

# Start VM and connect to install Windows
Start-VM -Name "BaseImage-Setup"
vmconnect localhost "BaseImage-Setup"
```

**Step 3: Configure Windows inside the VM:**

1. Install Windows Server 2022 (or your preferred version)
2. Complete Windows Setup
3. Install Windows Updates
4. Enable PowerShell Remoting:
   ```powershell
   Enable-PSRemoting -Force
   ```
5. Install any common tools you want (optional)
6. Shut down the VM from inside Windows

**Step 4: Remove the temporary VM:**

```powershell
# Remove VM (keeps the VHD)
Remove-VM -Name "BaseImage-Setup" -Force
```

Your base image is now ready at `C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx`!

#### Option 3: Quick Test (Skip Image Creation)

For testing Boxy without creating a full Windows base image, you can:

1. Use the scratch provider instead (see `examples/00-quickstart-scratch`)
2. Or create a minimal Linux-based VHD (not covered in this guide)

## Running This Example

### 1. Build Boxy

```powershell
# From the boxy repository root
go build -o boxy.exe ./cmd/boxy
```

### 2. Start the Server

```powershell
# Run as Administrator
.\boxy.exe serve --config examples\04-hyperv-local\boxy.yaml
```

You should see:

```
INFO[0000] Hyper-V provider registered
INFO[0000] Starting pool manager                         pool=windows-vm-pool
INFO[0000] Starting sandbox manager
INFO[0000] HTTP API server listening                     addr=:8080
```

The server will start provisioning 1 VM (min_ready=1) in the background.

### 3. Create a Sandbox

Open a **new PowerShell window as Administrator**:

```powershell
# Create a sandbox with 1 VM from the pool
.\boxy.exe sandbox create `
    --config examples\04-hyperv-local\boxy.yaml `
    -p windows-vm-pool:1 `
    -d 2h `
    --name my-dev-vm
```

You'll see output like:

```
✓ Sandbox 7af51049 created (allocating resources...)
Waiting for resources ✓

✓ Sandbox ready

ID:         7af51049-5fe7-414b-9bbe-307f496a0945
Name:       my-dev-vm
Resources:  1
State:      ready
Expires:    2025-11-25T16:00:00Z (in 1h59m)

Resource Details:
─────────────────

[1] Resource 1c8f41b1
    Type: rdp
    Host: 192.168.1.123
    Username: Administrator
    Password: P@ssABcd1234XYZ
```

### 4. Connect to the VM

Use Remote Desktop Connection:

```powershell
# Use the credentials from the output above
mstsc /v:192.168.1.123
```

Or save to RDP file:

```powershell
# Create RDP file
@"
full address:s:192.168.1.123:3389
username:s:Administrator
"@ | Out-File -Encoding ASCII my-dev-vm.rdp

# Connect
mstsc my-dev-vm.rdp
```

### 5. List Active Sandboxes

```powershell
.\boxy.exe sandbox list --config examples\04-hyperv-local\boxy.yaml
```

### 6. Destroy the Sandbox

```powershell
# When you're done
.\boxy.exe sandbox destroy 7af51049 --config examples\04-hyperv-local\boxy.yaml
```

The VM will be stopped, removed, and the differencing disk deleted. The base image remains intact for future use.

## How It Works

### Embedded Agent Mode

Unlike the remote agent example (`03-remote-agent`), this configuration runs everything in a single process:

```
┌─────────────────────────────────────┐
│   Boxy Server Process               │
│                                     │
│   ┌──────────────────────────┐     │
│   │  Sandbox Manager         │     │
│   └──────────────────────────┘     │
│              │                      │
│   ┌──────────────────────────┐     │
│   │  Pool Manager            │     │
│   └──────────────────────────┘     │
│              │                      │
│   ┌──────────────────────────┐     │
│   │  Hyper-V Provider        │     │
│   │  (local, embedded)       │     │
│   └──────────────────────────┘     │
│              │                      │
└──────────────┼──────────────────────┘
               │
        ┌──────▼──────┐
        │   Hyper-V   │
        │   VMMs      │
        └─────────────┘
```

### Preheating (min_ready: 1)

With `min_ready: 1`, Boxy keeps 1 VM provisioned and ready at all times:

- When the server starts, it provisions 1 VM immediately
- When you allocate a VM to a sandbox, it's **instant** (VM is already running)
- Boxy immediately provisions a new VM to replace the allocated one
- Pool always maintains at least 1 ready VM

This means:
- First sandbox creation: **Instant** (uses pre-warmed VM)
- Subsequent creations: **Instant** (while pool < max_total)

### Differencing Disks

Boxy uses Hyper-V differencing disks for fast provisioning:

```
C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx  <-- Base image (read-only)
                                  │
                ┌─────────────────┼─────────────────┐
                │                 │                 │
         VM-1 (diff)         VM-2 (diff)      VM-3 (diff)
         2GB changes         500MB changes    1.5GB changes
```

Benefits:
- Provisioning time: ~30 seconds (vs 10+ minutes copying full disk)
- Disk usage: Only stores changes from base image
- Base image never modified

## Configuration Options

### Custom Paths

To use different directories (e.g., D: drive for more space):

```yaml
pools:
  - name: windows-vm-pool
    backend: hyperv
    # ... other settings ...

    extra_config:
      vm_path: "D:\\Boxy\\VMs"
      vhd_path: "D:\\Boxy\\VHDs"
      base_images_path: "D:\\Boxy\\BaseImages"
      switch_name: "External Switch"
      default_generation: 2
```

### Virtual Switch

By default, Boxy uses "Default Switch". To use a different switch:

```yaml
extra_config:
  switch_name: "External Switch"
```

List available switches:

```powershell
Get-VMSwitch | Select-Object Name, SwitchType
```

### Multiple Images

You can configure multiple pools with different base images:

```yaml
pools:
  - name: win-server-2022
    backend: hyperv
    image: "windows-server-2022"
    min_ready: 1

  - name: win-server-2019
    backend: hyperv
    image: "windows-server-2019"
    min_ready: 1
```

## Troubleshooting

### "Hyper-V provider unavailable"

Ensure you're running as Administrator:

```powershell
# Check if running as admin
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
Write-Host "Running as Administrator: $isAdmin"
```

If false, restart PowerShell as Administrator.

### "Base image not found"

Check that your base image exists:

```powershell
Test-Path "C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx"
```

Update the `image` field in `boxy.yaml` to match your actual base image name.

### "Switch not found"

List available switches:

```powershell
Get-VMSwitch
```

Update `switch_name` in config to match an existing switch.

### VM doesn't get IP address

Check DHCP on your virtual switch:

```powershell
# Check VM network adapter
Get-VMNetworkAdapter -VMName "boxy-*"

# Use Default Switch (includes DHCP)
# Or configure DHCP on your custom switch
```

### Slow provisioning

- Ensure VHDs are on SSD storage
- Check host resource usage (CPU, memory, disk)
- Reduce `min_ready` if resource-constrained
- Consider using smaller base images

## Next Steps

- **Add hooks**: Customize VMs with software installation (see commented examples)
- **Multiple pools**: Configure different VM types for different workloads
- **Monitoring**: Enable HTTP API to monitor pool status
- **Remote agent**: Move Hyper-V to dedicated Windows server (see `03-remote-agent`)

## See Also

- [Hyper-V Provider Documentation](../../docs/providers/hyperv.md)
- [Hooks Documentation](../../docs/architecture/HOOKS.md)
- [Remote Agent Example](../03-remote-agent/)
