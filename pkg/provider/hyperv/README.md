# pkg/provider/hyperv

Hyper-V provider implementation for Boxy.

## Purpose

Implements the `provider.Provider` interface for Hyper-V VMs on Windows, enabling Boxy to provision and manage virtualized Windows/Linux environments.

## Contract

**Implements**: `provider.Provider`

**Input**:

- `provider.ResourceSpec` - VM specification (base image, CPUs, memory, disk)
- Context for cancellation/timeout

**Output**:

- `provider.Resource` - Provisioned VM with connection info
- Encrypted credentials in resource metadata

**Guarantees**:

- Differencing disks (VMs use base images without modification)
- Automatic VM naming (unique UUIDs)
- PowerShell Direct for command execution
- Clean teardown on provision failure
- IP address detection with configurable timeout

**Limitations**:

- **Windows only** - Requires Hyper-V feature enabled
- **Administrator privileges** - Hyper-V operations require elevation
- Base images must exist at configured path
- Single virtual switch (configured globally)

## Usage Example

```go
import (
    "context"
    "github.com/sirupsen/logrus"
    "boxy/pkg/crypto"
    "boxy/pkg/provider"
    "boxy/pkg/provider/hyperv"
)

// Create provider
logger := logrus.New()
key, _ := crypto.GenerateKey()
encryptor, _ := crypto.NewEncryptor(key)

config := hyperv.DefaultConfig()
config.BaseImagesPath = "C:\\BaseImages"
config.SwitchName = "External"

provider := hyperv.NewProviderWithConfig(logger, encryptor, config)

// Provision VM
spec := provider.ResourceSpec{
    Type:         provider.ResourceTypeVM,
    ProviderType: "hyperv",
    Image:        "windows-server-2022",  // Base VHD name
    CPUs:         4,
    MemoryMB:     8192,
    DiskGB:       100,
}

resource, err := provider.Provision(context.Background(), spec)
if err != nil {
    log.Fatal(err)
}

// Get connection info (RDP)
conn, err := provider.GetConnectionInfo(context.Background(), resource)
fmt.Printf("RDP: %s:%d\n", conn.Host, conn.Port)
fmt.Printf("Username: %s\n", conn.Username)
fmt.Printf("Password: %s\n", conn.Password)

// Execute command inside VM (PowerShell Direct)
result, err := provider.Exec(context.Background(), resource, []string{
    "powershell", "-Command", "Get-ComputerInfo",
})

// Cleanup
provider.Destroy(context.Background(), resource)
```

## Architecture

**Links:**

- [Provider Interface](../provider.go)
- [Package Reorganization](../../../docs/planning/REORGANIZATION_STATUS.md)

**Dependencies:**

- `pkg/provider` - Provider interface & types
- `pkg/crypto` - Password generation & encryption
- `pkg/powershell` - PowerShell command execution

**Used by:**

- `internal/core/pool` - Pool management
- `internal/core/sandbox` - Sandbox orchestration

## Features

### Provisioning

- Creates differencing VHDs from base images
- Generates secure random passwords
- Configures VM (CPUs, memory, network)
- Starts VM automatically
- Waits for IP address (configurable timeout)
- Validates base images exist

### Destruction

- Stops VM (forcefully if needed)
- Removes VM configuration
- Deletes differencing VHD
- Handles already-stopped VMs gracefully

### Status

- VM state (running, stopped, etc.)
- Health checks
- Resource usage (CPU, memory, uptime)

### Connection

- Returns RDP connection details
- Provides IP address and credentials
- Decrypts passwords on-demand

### Execution

- PowerShell Direct (no network required!)
- Runs commands inside VMs
- Captures stdout/stderr and exit codes
- Supports credential-based authentication

### Update Operations

- Power state changes (start, stop, pause, reset)
- Snapshots (create, restore, delete)
- Resource limits (CPU, memory)

## Configuration

```go
config := &hyperv.Config{
    VMPath:            "C:\\Hyper-V\\VMs",
    VHDPath:           "C:\\Hyper-V\\VHDs",
    SwitchName:        "External",
    BaseImagesPath:    "C:\\BaseImages",
    DefaultGeneration: 2,  // Gen 2 VMs (UEFI)
    WaitForIPTimeout:  5 * time.Minute,
}
```

## Testing

### Unit Tests

TODO: Add unit tests with mocked PowerShell

```bash
go test -v ./pkg/provider/hyperv
```

### Integration Tests

Requires Windows + Hyper-V:

```bash
# Enable Hyper-V
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All

# Run as Administrator
go test -v ./pkg/provider/hyperv -tags=integration
```

**Test coverage needed:**

- [ ] Unit tests (mock pkg/powershell)
- [ ] Integration tests (real Hyper-V)
- [ ] Error scenarios (missing base images, permission errors)
- [ ] PowerShell Direct execution
- [ ] Snapshot operations

## Development

### Requirements

- Windows 10/11 Pro or Windows Server
- Hyper-V feature enabled
- Administrator privileges
- PowerShell 5.1+

### Setup

```powershell
# Enable Hyper-V
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All

# Create virtual switch
New-VMSwitch -Name "External" -NetAdapterName "Ethernet" -AllowManagementOS $true

# Prepare base images
mkdir C:\BaseImages
# Copy VHD/VHDX files to C:\BaseImages\
```

### Debugging

Enable debug logging:

```go
logger := logrus.New()
logger.SetLevel(logrus.DebugLevel)
provider := hyperv.NewProviderWithConfig(logger, encryptor, config)
```

Logs include:

- PowerShell scripts executed
- VM creation steps
- IP address detection retries
- Cleanup operations

## Common Issues

**"Hyper-V is not available on this system"**

- Enable Hyper-V feature
- Requires Windows Pro/Enterprise or Server
- Reboot after enabling

**"Base image not found"**

- Check `BaseImagesPath` configuration
- Ensure VHD/VHDX files exist
- Image name in spec must match filename (without extension)

**"Access denied" / Permission errors**

- Run as Administrator
- Check Hyper-V Administrators group membership

**VM doesn't get IP address**

- Check virtual switch configuration
- Ensure DHCP available on network
- Increase `WaitForIPTimeout`
- Check VM network adapter settings

**PowerShell Direct fails**

- VM must be running
- Integration Services must be installed in VM
- Credentials must be valid

## Future Enhancements

- [ ] Support for multiple virtual switches
- [ ] Network isolation per sandbox
- [ ] GPU passthrough configuration
- [ ] Nested virtualization support
- [ ] Automatic base image updates
- [ ] VM template management
- [ ] Support for Linux VMs
