# pkg/powershell

PowerShell executor for Go - execute PowerShell commands with proper marshalling and error handling.

## Purpose

Execute PowerShell scripts from Go code with structured output parsing, timeout support, and comprehensive error handling.

## Contract

**Input:**

- PowerShell script (string)
- Context (for cancellation/timeout)
- Optional: Result struct for JSON unmarshaling

**Output:**

- String output (stdout)
- Structured data (via JSON parsing)
- Errors with context (stderr, exit codes)

**Guarantees:**

- Context cancellation respected (timeouts work)
- Stderr captured and logged
- JSON parsing errors include output for debugging
- No shell injection (uses exec.CommandContext safely)

**Limitations:**

- Windows only (requires powershell.exe)
- Synchronous execution only
- No interactive commands (uses -NonInteractive)
- No persistent sessions (each call is independent)

## Usage Example

### Simple Execution

```go
import (
    "context"
    "github.com/sirupsen/logrus"
    "boxy/pkg/powershell"
)

logger := logrus.New()
exec := powershell.New(logger)

// Execute simple command
output, err := exec.Exec(context.Background(), "Get-Date")
if err != nil {
    log.Fatal(err)
}
fmt.Println(output)
```

### JSON Output

```go
type VMInfo struct {
    Name  string `json:"Name"`
    State string `json:"State"`
}

script := `Get-VM -Name "TestVM" | Select-Object Name, State | ConvertTo-Json`

var vm VMInfo
err := exec.ExecJSON(context.Background(), script, &vm)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("VM %s is %s\n", vm.Name, vm.State)
```

### Timeout Support

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

output, err := exec.Exec(ctx, "Long-Running-Command")
// Will cancel after 30 seconds
```

## Architecture

**Links:**

- [Package Restructure Plan](../../docs/planning/PACKAGE_RESTRUCTURE.md)
- [ADR-006: Package Organization](../../docs/decisions/adr-006-package-organization.md) (when created)

**Dependencies:**

- Standard library: `os/exec`, `context`, `encoding/json`
- `github.com/sirupsen/logrus` - Logging

**Used by:**

- `pkg/hyperv` - Hyper-V operations via PowerShell
- `pkg/hyperv/psdirect` - PowerShell Direct (exec in VMs)
- `pkg/provider/hyperv` - Hyper-V provider implementation

## Testing

### Unit Tests

Run on Windows only:

```bash
go test ./pkg/powershell
```

Tests cover:

- ✅ Basic execution (echo, arithmetic)
- ✅ JSON parsing (objects, arrays, typed structs)
- ✅ Error handling (command failure, invalid JSON)
- ✅ Context cancellation/timeout
- ✅ Stderr handling (warnings)

### Platform Requirements

- **Windows only** - Tests skip on Linux/macOS
- PowerShell 5.1+ (included in Windows 10+)

### CI Strategy

- Windows CI runners: Full test suite
- Linux CI runners: Tests automatically skipped

## Development

### Running Tests

```bash
# On Windows
go test -v ./pkg/powershell

# With coverage
go test -v -cover ./pkg/powershell

# Skip on Linux/macOS (automatic)
go test ./pkg/powershell  # Tests skipped
```

### Debugging

Enable debug logging:

```go
logger := logrus.New()
logger.SetLevel(logrus.DebugLevel)
exec := powershell.New(logger)
```

Logs include:

- Script length
- Stdout/stderr
- Execution time
- Errors with full context

### Common Issues

**"powershell.exe not found"**

- Windows only, or `powershell.exe` not in PATH

**Timeout errors**

- Increase context timeout
- Check for hung commands
- Use `-NoProfile` to skip profile loading (already included)

**JSON parsing errors**

- Ensure PowerShell script uses `ConvertTo-Json`
- Check for warnings/errors mixed in output
- Use `-Compress` to avoid formatting issues

## Future Enhancements

- [ ] Support for pwsh (PowerShell Core / 7+)
- [ ] Persistent PowerShell sessions (runspaces)
- [ ] Async execution with streaming output
- [ ] Parameter marshalling (safer than string interpolation)
- [ ] Mock executor for testing consumers
