# pkg/hyperv/psdirect

PowerShell Direct client for executing commands inside Hyper-V VMs.

## Purpose

PowerShell Direct allows executing commands inside Hyper-V VMs without network connectivity, using VM integration services. This is useful for:

- Initial VM configuration before network is available
- Running commands on VMs without network access
- Executing privileged operations inside VMs

## Contract

**Requires**:

- Windows host with Hyper-V
- VM must be running
- VM must have integration services installed
- Valid credentials for the VM

**Provides**:

- Secure command execution (commands passed as data, not code)
- Protection against PowerShell injection attacks
- Support for any command/executable inside the VM

## Usage Example

```go
import (
    "context"
    "github.com/Geogboe/boxy/pkg/hyperv/psdirect"
    "github.com/Geogboe/boxy/pkg/powershell"
    "github.com/sirupsen/logrus"
)

// Create client
ps := powershell.New(logrus.New())
client := psdirect.NewClient(ps, logrus.New())

// Create credentials
creds := psdirect.NewCredentials("Administrator", "MyPassword123!")

// Execute command
result, err := client.Exec(
    context.Background(),
    "MyVM",
    creds,
    []string{"powershell", "-Command", "Get-ComputerInfo"},
)

if err != nil {
    log.Fatal(err)
}

fmt.Printf("Exit code: %d\n", result.ExitCode)
fmt.Printf("Output: %s\n", result.Stdout)
```

## Security

Commands are passed as **data** via PowerShell's `-ArgumentList` parameter, not embedded in the ScriptBlock. This prevents injection attacks where malicious input could break out of the command context.

**Safe approach (used here)**:

```powershell
$cmdArray = @('powershell', '-Command', 'Get-Process')
Invoke-Command -VMName 'VM' -Credential $cred -ScriptBlock {
    param([string[]]$command)
    & $command[0] $command[1..($command.Length-1)]
} -ArgumentList (,$cmdArray)
```

**Unsafe approach (NOT used)**:

```powershell
# Don't do this! User input embedded in code
Invoke-Command -VMName 'VM' -Credential $cred -ScriptBlock {
    & $userInput  # Injection risk!
}
```

## Testing

```bash
# Unit tests (with mocked PowerShell)
go test -v .

# Integration tests (requires Windows + Hyper-V + running VM)
go test -v . -tags=integration
```

## Dependencies

- `pkg/powershell` - PowerShell execution
- No other dependencies

## Architecture

**Used by**:

- `pkg/provider/hyperv` - Hyper-V provider uses this for command execution

**Links**:

- [PowerShell Direct Documentation](https://docs.microsoft.com/en-us/virtualization/hyper-v-on-windows/user-guide/powershell-direct)
