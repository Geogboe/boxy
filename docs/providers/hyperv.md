# Hyper-V Provider

The Hyper-V provider enables Boxy to provision and manage Windows virtual machines using Microsoft Hyper-V.

## Features

- ✅ **VM Provisioning** - Create VMs from base images using differencing disks
- ✅ **Power Management** - Start, stop, pause, and restart VMs
- ✅ **Snapshots** - Create, restore, and delete VM checkpoints
- ✅ **PowerShell Direct** - Execute commands inside VMs without network connectivity
- ✅ **RDP Access** - Automatic credential generation and RDP connection info
- ✅ **Resource Management** - Adjust CPU and memory allocation
- ✅ **Fast Provisioning** - Differencing disks for instant VM cloning

## Requirements

### System Requirements

- **Operating System**: Windows Server 2016+ or Windows 10/11 Pro/Enterprise
- **Hyper-V Role**: Installed and enabled
- **PowerShell**: Version 5.1 or later
- **Privileges**: Administrator rights required

### Enabling Hyper-V

**Windows 10/11:**
```powershell
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All
```

**Windows Server:**
```powershell
Install-WindowsFeature -Name Hyper-V -IncludeManagementTools -Restart
```

### Verifying Hyper-V

```powershell
# Check if Hyper-V is running
Get-Service vmms

# Check Hyper-V status
Get-VMHost
```

## Configuration

### Provider Configuration

```yaml
# In boxy.yaml or agent configuration
pools:
  - name: windows-pool
    type: vm
    backend: hyperv
    image: "windows-server-2022"  # Name of base image
    min_ready: 2
    max_total: 10
    cpus: 2
    memory_mb: 4096
    disk_gb: 60

    timeouts:
      provision: 15m        # VM creation can take time
      finalization: 10m
      personalization: 5m
```

### Custom Hyper-V Configuration

The Hyper-V provider can be configured with custom paths and settings:

```go
// In code (for custom configurations)
config := &hyperv.Config{
    VMPath:            "D:\\VMs",                    // Where VM configs are stored
    VHDPath:           "D:\\VHDs",                   // Where virtual disks are stored
    SwitchName:        "External Switch",           // Virtual switch to use
    BaseImagesPath:    "D:\\BaseImages",            // Base VHD images location
    DefaultGeneration: 2,                           // VM generation (1 or 2)
    WaitForIPTimeout:  5 * time.Minute,            // How long to wait for IP
}

provider := hyperv.NewProviderWithConfig(logger, encryptor, config)
```

**Default Paths:**
- VM Path: `C:\ProgramData\Boxy\VMs`
- VHD Path: `C:\ProgramData\Boxy\VHDs`
- Base Images: `C:\ProgramData\Boxy\BaseImages`
- Switch: `Default Switch`

## Base Images

### Creating Base Images

Boxy uses **differencing disks** for fast provisioning. You need to create base VHD images that will serve as templates.

**Step 1: Create a base VHD with Windows installed:**

```powershell
# Create a base VHD (60GB)
New-VHD -Path "C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx" `
    -SizeBytes 60GB -Dynamic

# Create a VM to install Windows
New-VM -Name "BaseImage-WinServer2022" `
    -MemoryStartupBytes 4GB `
    -Generation 2 `
    -VHDPath "C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx"

# Add DVD drive for installation media
Add-VMDvdDrive -VMName "BaseImage-WinServer2022" `
    -Path "D:\ISOs\WindowsServer2022.iso"

# Start VM and install Windows
Start-VM -Name "BaseImage-WinServer2022"
```

**Step 2: Configure the base image:**

Inside the VM:
1. Install Windows
2. Install VM guest integration services (usually included)
3. Run Windows Update
4. Install any common software (browsers, tools, etc.)
5. **Enable PowerShell Remoting**:
   ```powershell
   Enable-PSRemoting -Force
   ```
6. Generalize with Sysprep (optional, for multi-use images):
   ```cmd
   C:\Windows\System32\Sysprep\sysprep.exe /generalize /oobe /shutdown
   ```

**Step 3: Shut down and remove the VM:**

```powershell
# Stop VM
Stop-VM -Name "BaseImage-WinServer2022"

# Remove VM but keep the VHD
Remove-VM -Name "BaseImage-WinServer2022" -Force
```

**Step 4: The base image is ready!**

The VHD at `C:\ProgramData\Boxy\BaseImages\windows-server-2022.vhdx` can now be used as a base image. When Boxy provisions a VM, it creates a **differencing disk** that uses this base image as a parent, allowing instant "cloning" without copying the entire disk.

### Example Base Images

```
C:\ProgramData\Boxy\BaseImages\
├── windows-server-2022.vhdx      (Server 2022 with updates)
├── windows-server-2019.vhdx      (Server 2019 with updates)
├── windows-11-pro.vhdx           (Windows 11 Pro)
└── windows-10-enterprise.vhdx    (Windows 10 Enterprise)
```

## Network Configuration

### Virtual Switches

Boxy VMs need network connectivity. Configure a virtual switch:

**Create External Switch** (recommended for internet access):
```powershell
New-VMSwitch -Name "External Switch" `
    -NetAdapterName "Ethernet" `
    -AllowManagementOS $true
```

**Create Internal Switch** (for isolated networks):
```powershell
New-VMSwitch -Name "Internal Switch" -SwitchType Internal
```

**Use Default Switch** (Windows 10/11):
- The "Default Switch" provides NAT and DHCP automatically
- VMs get internet access but are not accessible from outside
- Good for development and testing

### IP Address Assignment

Boxy waits for VMs to get an IP address via DHCP. Configure DHCP on your virtual switch or use the Default Switch which includes DHCP.

If a VM doesn't get an IP within the timeout, provisioning continues but `GetConnectionInfo` may fail until the VM gets an IP.

## PowerShell Direct

PowerShell Direct allows Boxy to execute commands inside VMs **without network connectivity** using the VM bus.

**Requirements:**
- Windows host
- Windows guest (same or newer version as host)
- VM Integration Services enabled
- Guest must be fully booted

**Benefits:**
- Works even if network is down
- No firewall configuration needed
- More secure than network-based remoting

**Example Hook Using PowerShell Direct:**
```yaml
hooks:
  after_provision:
    - name: install-tools
      shell: powershell
      inline: |
        Install-WindowsFeature -Name Web-Server
        Install-WindowsFeature -Name RSAT-AD-Tools
      timeout: 10m
```

Boxy automatically uses PowerShell Direct via `Invoke-Command -VMName`.

## Credentials

### Automatic Generation

Boxy automatically generates secure random passwords for each VM:
- Username: `Administrator`
- Password: Cryptographically secure 16-character password
- Stored encrypted in database

### Retrieving Credentials

```bash
# Get connection info for a sandbox
boxy sandbox get sb-12345

# Output includes:
# - RDP host (VM IP address)
# - Port (3389)
# - Username (Administrator)
# - Password (decrypted)
```

### Custom Credentials (Future Enhancement)

Currently, credentials are auto-generated. Future versions may support:
- User-provided passwords
- Active Directory integration
- SSH key authentication

## VM Lifecycle

### Provision

1. Generate unique VM name and credentials
2. Create differencing disk from base image
3. Create VM with specified CPU/memory
4. Connect to virtual switch
5. Start VM
6. Wait for IP address (with timeout)
7. Return resource with connection info

**Provisioning Time**: ~30 seconds (differencing disk creation is fast)

### Destroy

1. Stop VM (forced, immediate)
2. Remove VM configuration
3. Delete differencing disk (base image remains intact)

**Cleanup Time**: ~5 seconds

### Updates

Boxy supports runtime updates to VMs:

**Power State:**
- `running` - Start VM
- `stopped` - Stop VM (forced)
- `paused` - Suspend VM
- `reset` - Restart VM

**Snapshots:**
- `create` - Create checkpoint
- `restore` - Restore from checkpoint
- `delete` - Remove checkpoint

**Resources:**
- Adjust CPU count (VM must be stopped)
- Adjust memory (VM must be stopped for Generation 2 VMs)

## Troubleshooting

### Provider Health Check Fails

**Error**: `Hyper-V Virtual Machine Management service not found`

**Solution**: Install Hyper-V role:
```powershell
Install-WindowsFeature -Name Hyper-V -IncludeManagementTools -Restart
```

---

**Error**: `Hyper-V Virtual Machine Management service is not running`

**Solution**: Start the service:
```powershell
Start-Service vmms
```

### Provisioning Fails

**Error**: `failed to create VHD: parent path not found`

**Solution**: Ensure base image exists:
```powershell
Test-Path "C:\ProgramData\Boxy\BaseImages\your-image.vhdx"
```

---

**Error**: `failed to create VM: switch not found`

**Solution**: Create the virtual switch or update config:
```powershell
# List available switches
Get-VMSwitch

# Create a switch
New-VMSwitch -Name "Default Switch" -SwitchType Internal
```

---

**Error**: `timeout waiting for VM IP address`

**Cause**: VM didn't get IP within timeout

**Solutions**:
1. Check DHCP is available on the virtual switch
2. Use "Default Switch" which includes DHCP
3. Increase timeout in config:
   ```yaml
   # Custom timeout (not yet supported in YAML, requires code change)
   ```
4. VMs can still be used, IP will be available later

### PowerShell Direct Fails

**Error**: `Invoke-Command failed: cannot connect to VM`

**Possible Causes:**
1. VM not fully booted yet
2. Guest integration services not installed
3. Different OS version between host and guest
4. VM credentials incorrect

**Solutions**:
1. Wait for VM to fully boot (check with `Get-VM`)
2. Install/update guest integration services in the VM
3. Ensure guest OS is Windows and compatible version
4. Verify credentials are correct

### Permission Denied

**Error**: `Access is denied` when running Boxy commands

**Solution**: Run Boxy with Administrator privileges:
```powershell
# Run as Administrator
# Right-click PowerShell/CMD -> Run as Administrator
```

### VHD Path Too Long

**Error**: Path length exceeds Windows limit

**Solution**: Use shorter paths or move Boxy data closer to root:
```go
config := &hyperv.Config{
    VMPath:  "D:\\VMs",
    VHDPath: "D:\\VHDs",
}
```

## Performance Optimization

### Use SSD Storage

Store VHDs on SSD for better performance:
- Base images on SSD
- Differencing disks on SSD
- Significant improvement in VM boot time

### Differencing Disk Benefits

Boxy uses differencing disks by default:
- ✅ Instant provisioning (seconds vs minutes)
- ✅ Minimal disk space (only stores changes)
- ✅ Base image is never modified
- ✅ Easy to refresh VMs (just delete differencing disk)

### Resource Allocation

**Recommendations:**
- Don't over-provision CPU (Hyper-V will time-slice)
- Leave memory for host OS (at least 4GB for Hyper-V host)
- Use dynamic memory for better utilization
- Monitor host resource usage

### Pool Configuration

```yaml
pools:
  - name: fast-pool
    backend: hyperv
    min_ready: 5          # Keep 5 VMs warm
    max_total: 20         # Don't exceed 20 total

    # Smaller VMs = more capacity
    cpus: 2
    memory_mb: 2048      # 2GB instead of 4GB
```

## Security Considerations

### Credential Security

- ✅ Passwords encrypted at rest (AES-256-GCM)
- ✅ Secure random generation
- ✅ Never logged
- ⚠️ Transmitted in plain text over RDP (use TLS)

### Network Isolation

- Use internal switches for isolated environments
- External switches for internet access
- Configure firewall rules as needed
- Consider VLANs for multi-tenant scenarios

### Base Image Security

- Keep base images updated (Windows Update)
- Remove unnecessary software
- Disable unused services
- Consider security hardening (CIS benchmarks)
- Regularly rebuild base images

### PowerShell Remoting

- PowerShell Direct is more secure than network remoting
- Uses VM bus, not network
- Still requires authentication
- Consider disabling network PS remoting in base image

## Limitations

### Current Limitations

1. **Windows Only** - Hyper-V only runs on Windows
2. **Windows Guests** - PowerShell Direct only works with Windows guests
3. **Fixed Credentials** - Only Administrator account supported
4. **No Dynamic Memory** - Fixed memory allocation at provision time
5. **No Live Migration** - VMs are tied to one host
6. **No HA** - No automatic failover

### Future Enhancements

- [ ] Dynamic memory support
- [ ] Custom credentials (user-provided or AD-integrated)
- [ ] Linux guest support (limited, no PowerShell Direct)
- [ ] VLAN configuration
- [ ] Nested virtualization support
- [ ] GPU passthrough support

## Examples

### Basic Pool Configuration

```yaml
pools:
  - name: win-server-pool
    type: vm
    backend: hyperv
    image: "windows-server-2022"
    min_ready: 2
    max_total: 5
    cpus: 2
    memory_mb: 4096
    disk_gb: 60
```

### With Hooks

```yaml
pools:
  - name: web-server-pool
    type: vm
    backend: hyperv
    image: "windows-server-2022"
    min_ready: 1
    max_total: 3
    cpus: 4
    memory_mb: 8192

hooks:
  after_provision:
    - name: install-iis
      shell: powershell
      inline: |
        Install-WindowsFeature -Name Web-Server -IncludeManagementTools
        Install-WindowsFeature -Name Web-Asp-Net45
      timeout: 10m

  before_allocate:
    - name: configure-site
      shell: powershell
      inline: |
        New-Website -Name "BoxySite-${resource.id}" `
          -PhysicalPath "C:\inetpub\wwwroot" `
          -Port 80
      timeout: 1m
```

### Remote Agent Configuration

```yaml
# Agent on Windows machine (agent-config.yaml)
logging:
  level: info

# Server on Linux machine (server-config.yaml)
agents:
  - id: windows-hyperv
    address: "192.168.1.100:50051"
    providers: ["hyperv"]
    use_tls: false

pools:
  - name: remote-hyperv-pool
    backend: windows-hyperv-hyperv
    image: "windows-server-2022"
    min_ready: 2
```

## Best Practices

1. **Keep base images updated** - Monthly rebuild recommended
2. **Use differencing disks** - Default behavior, don't change
3. **Monitor resources** - Check host CPU/memory usage
4. **Clean up snapshots** - They consume disk space
5. **Use SSD storage** - Significant performance improvement
6. **Secure base images** - Apply security hardening
7. **Test provisioning** - Verify base images work before deploying
8. **Document images** - Keep notes on what's installed in each base image
9. **Backup base images** - They're valuable, back them up
10. **Use appropriate switch** - External for internet, Internal for isolation

## Testing

### Unit Tests

```bash
# Run unit tests
go test ./internal/provider/hyperv/...
```

### Integration Tests

```bash
# Run integration tests (requires Windows + Hyper-V)
go test -tags windows ./tests/integration/...
```

**Integration test requirements:**
- Windows OS
- Hyper-V installed
- Administrator privileges
- Base image at `C:\ProgramData\Boxy\BaseImages\test-image.vhdx`

## See Also

- [Agent Architecture](../architecture/distributed-agent-implementation.md)
- [Example: Remote Agent](../../examples/03-remote-agent/README.md)
- [Security Guide](../architecture/security-guide.md)
- [Hyper-V Documentation](https://docs.microsoft.com/en-us/virtualization/hyper-v-on-windows/)
