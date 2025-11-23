For an overview of the entire system architecture, please refer to the [Boxy v1 Complete Architecture Map](../ARCHITECTURE_MAP.md).

# Resource Interaction Guide

## Overview

After provisioning a resource, users need to interact with it. Each provider implements a method for executing commands inside the resource.

## Provider Interface: Execute Method

```go
// Execute runs a command inside the resource and returns the output
Execute(ctx context.Context, res *resource.Resource, cmd []string) (*ExecuteResult, error)

type ExecuteResult struct {
    ExitCode int    // Command exit code
    Stdout   string // Standard output
    Stderr   string // Standard error
    Error    error  // Execution error (connection failed, etc.)
}
```

## Provider-Specific Implementations

### Docker Provider

**Mechanism**: `docker exec`

**Implementation**:

```go
func (p *DockerProvider) Execute(ctx context.Context, res *resource.Resource, cmd []string) (*ExecuteResult, error) {
    // Create exec instance
    execConfig := types.ExecConfig{
        AttachStdout: true,
        AttachStderr: true,
        Cmd:          cmd,
    }

    execID, err := p.client.ContainerExecCreate(ctx, res.ProviderID, execConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to create exec: %w", err)
    }

    // Attach and run
    resp, err := p.client.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
    if err != nil {
        return nil, fmt.Errorf("failed to attach exec: %w", err)
    }
    defer resp.Close()

    // Read output
    stdout, stderr := captureOutput(resp.Reader)

    // Get exit code
    inspect, err := p.client.ContainerExecInspect(ctx, execID.ID)
    if err != nil {
        return nil, fmt.Errorf("failed to inspect exec: %w", err)
    }

    return &ExecuteResult{
        ExitCode: inspect.ExitCode,
        Stdout:   stdout,
        Stderr:   stderr,
    }, nil
}
```

**Usage**:

```go
result, err := provider.Execute(ctx, resource, []string{"ls", "-la", "/app"})
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Exit Code: %d\n", result.ExitCode)
fmt.Printf("Output:\n%s\n", result.Stdout)
```

### Hyper-V Provider

**Mechanism**: PowerShell Direct (Invoke-Command)

PowerShell Direct allows running commands in Hyper-V VMs without network connectivity. Requires:

- VM is running
- Integration Services enabled
- Credentials for VM

**Implementation**:

```go
func (p *HyperVProvider) Execute(ctx context.Context, res *resource.Resource, cmd []string) (*ExecuteResult, error) {
    vmName := res.ProviderID // VM name

    // Get credentials for VM from resource metadata
    username := res.Metadata["username"].(string)
    password, err := p.decryptPassword(res.Metadata["password_encrypted"].(string))
    if err != nil {
        return nil, fmt.Errorf("failed to decrypt password: %w", err)
    }

    // Build PowerShell Direct command
    // Invoke-Command -VMName <name> -Credential <cred> -ScriptBlock { <cmd> }

    psScript := buildPowerShellDirectCommand(vmName, username, password, cmd)

    // Execute PowerShell
    result, err := p.executePowerShell(ctx, psScript)
    if err != nil {
        return nil, fmt.Errorf("PowerShell Direct failed: %w", err)
    }

    return parseExecuteResult(result), nil
}

func buildPowerShellDirectCommand(vmName, username, password string, cmd []string) string {
    // Join cmd into single command
    cmdStr := strings.Join(cmd, " ")

    // Build secure string for password
    psScript := fmt.Sprintf(`
        $password = ConvertTo-SecureString -String '%s' -AsPlainText -Force
        $cred = New-Object System.Management.Automation.PSCredential('%s', $password)

        Invoke-Command -VMName '%s' -Credential $cred -ScriptBlock {
            %s
        }
    `, password, username, vmName, cmdStr)

    return psScript
}

func (p *HyperVProvider) executePowerShell(ctx context.Context, script string) (string, error) {
    cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()

    return stdout.String(), err
}
```

**Usage**:

```go
// Execute command in Hyper-V VM via PowerShell Direct
result, err := provider.Execute(ctx, resource, []string{"Get-Service"})
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Services:\n%s\n", result.Stdout)
```

**Alternative: SSH** (if PowerShell Direct not available)

```go
func (p *HyperVProvider) Execute(ctx context.Context, res *resource.Resource, cmd []string) (*ExecuteResult, error) {
    // Fall back to SSH if PowerShell Direct fails
    // Requires VM to have network connectivity

    connInfo, err := p.GetConnectionInfo(ctx, res)
    if err != nil {
        return nil, err
    }

    return p.executeViaSSH(ctx, connInfo, cmd)
}
```

## CLI Usage

```bash
# Execute command in a resource
boxy resource exec <resource-id> -- ls -la

# Execute in specific resource in a sandbox
boxy sandbox exec <sandbox-id> --resource <resource-id> -- /bin/bash -c "echo hello"

# Interactive shell
boxy resource shell <resource-id>

# For Docker containers
boxy resource exec res-abc123 -- apt-get update

# For Hyper-V VMs (via PowerShell Direct)
boxy resource exec res-xyz789 -- Get-Process
```

## API Usage

**REST API**:

```http
POST /api/v1/resources/{id}/exec
Content-Type: application/json

{
  "command": ["ls", "-la", "/app"],
  "timeout": 30
}

Response:
{
  "exit_code": 0,
  "stdout": "total 48\ndrwxr-xr-x  5 root root 4096 Nov 20 10:00 .\n...",
  "stderr": "",
  "duration_ms": 123
}
```

**Go SDK**:

```go
import "github.com/Geogboe/boxy/pkg/client"

client := client.New("https://boxy-server:8443")
result, err := client.Resources.Execute(ctx, "res-abc123", []string{"ls", "-la"})
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Exit Code: %d\n", result.ExitCode)
fmt.Println(result.Stdout)
```

## Security Considerations

### Credential Management

**Docker**:

- Uses existing container runtime auth
- No additional credentials needed
- Inherits container user context

**Hyper-V**:

- Requires VM credentials (username/password)
- Credentials encrypted at rest
- Passed securely via PowerShell Direct
- Never logged in plaintext

### Command Injection Prevention

```go
func validateCommand(cmd []string) error {
    if len(cmd) == 0 {
        return errors.New("empty command")
    }

    // Don't allow shell metacharacters in simple mode
    dangerous := []string{ ";", "|", "&", "$", "`", ">", "<"}
    for _, arg := range cmd {
        for _, char := range dangerous {
            if strings.Contains(arg, char) {
                return fmt.Errorf("potentially dangerous character in command: %s", char)
            }
        }
    }

    return nil
}
```

**Safe**:

```go
// Array form - safe
Execute(ctx, res, []string{"ls", "-la", "/path/with spaces"})
```

**Unsafe** (don't allow):

```go
// String form with shell - dangerous
Execute(ctx, res, []string{"/bin/sh", "-c", "ls -la; rm -rf /"})
```

### Audit Logging

```go
// Log all command executions
logger.WithFields(logrus.Fields{
    "resource_id": res.ID,
    "pool_id":     res.PoolID,
    "sandbox_id":  res.SandboxID,
    "command":     cmd,  // Log command but not output (may contain secrets)
    "exit_code":   result.ExitCode,
    "user":        userID,
}).Info("Command executed in resource")
```

## Implementation Checklist

### Docker Provider Checklist

- [x] Basic docker exec implementation (already works)
- [ ] Add Execute method to interface
- [ ] Handle stdin for interactive commands
- [ ] Stream output for long-running commands
- [ ] Add timeout support

### Hyper-V Provider Checklist

- [ ] Implement PowerShell Direct execution
- [ ] Handle credential management
- [ ] Add fallback to SSH
- [ ] Handle Windows-specific exit codes
- [ ] Test with various PowerShell commands

### CLI Commands

- [ ] `boxy resource exec` command
- [ ] `boxy sandbox exec` command (exec in sandbox resource)
- [ ] Interactive shell support
- [ ] Stdin/stdout/stderr streaming
- [ ] Timeout and cancellation

### API Endpoints

- [ ] POST /api/v1/resources/:id/exec
- [ ] WebSocket for interactive sessions
- [ ] Request validation
- [ ] Rate limiting
- [ ] Audit logging

## Testing

### Unit Tests

```go
func TestDockerExecute(t *testing.T) {
    provider := NewMockDockerProvider()
    res := &resource.Resource{ProviderID: "container-123"}

    result, err := provider.Execute(context.Background(), res, []string{"echo", "hello"})
    assert.NoError(t, err)
    assert.Equal(t, 0, result.ExitCode)
    assert.Contains(t, result.Stdout, "hello")
}

func TestHyperVExecute(t *testing.T) {
    if runtime.GOOS != "windows" {
        t.Skip("Hyper-V tests require Windows")
    }

    provider := NewHyperVProvider()
    res := testProvisionVM(t) // Helper to provision test VM
    defer provider.Destroy(context.Background(), res)

    result, err := provider.Execute(context.Background(), res, []string{"Get-Process"})
    assert.NoError(t, err)
    assert.Equal(t, 0, result.ExitCode)
    assert.NotEmpty(t, result.Stdout)
}
```

### Integration Tests

```go
func TestExecuteViaAgent(t *testing.T) {
    // Start agent with Docker provider
    agent := startTestAgent(t, "docker")
    defer agent.Stop()

    // Create remote provider
    remote := newRemoteProvider(t, agent.Address(), "docker")

    // Provision resource via remote
    res := testProvisionContainer(t, remote)
    defer remote.Destroy(context.Background(), res)

    // Execute command via remote provider
    result, err := remote.Execute(context.Background(), res, []string{"ls", "-la"})
    assert.NoError(t, err)
    assert.Equal(t, 0, result.ExitCode)
}
```

## Future Enhancements

### Streaming Output

```go
// Stream output for long-running commands
type OutputStream interface {
    Write(chunk []byte) error
    Close() error
}

func (p *Provider) ExecuteStream(
    ctx context.Context,
    res *resource.Resource,
    cmd []string,
    stdout, stderr OutputStream,
) error
```

### Interactive Sessions

```go
// Interactive shell with stdin/stdout/stderr
type Session interface {
    Stdin() io.Writer
    Stdout() io.Reader
    Stderr() io.Reader
    Wait() error
    Close() error
}

func (p *Provider) NewSession(
    ctx context.Context,
    res *resource.Resource,
) (Session, error)
```

### File Transfer

```go
// Copy files to/from resources
func (p *Provider) CopyTo(
    ctx context.Context,
    res *resource.Resource,
    localPath, remotePath string,
) error

func (p *Provider) CopyFrom(
    ctx context.Context,
    res *resource.Resource,
    remotePath, localPath string,
) error
```

## Examples

### Running Tests in Isolated Containers

```bash
# Provision container
sandbox_id=$(boxy sandbox create -p ubuntu-containers:1 --json | jq -r .id)
resource_id=$(boxy sandbox get $sandbox_id --json | jq -r '.resources[0].id')

# Install dependencies
boxy resource exec $resource_id -- apt-get update
boxy resource exec $resource_id -- apt-get install -y python3 python3-pip

# Run tests
boxy resource exec $resource_id -- python3 -m pytest /app/tests

# Cleanup
boxy sandbox destroy $sandbox_id
```

### Configuring Hyper-V VM

```bash
# Provision VM
sandbox_id=$(boxy sandbox create -p win-server-vms:1 --json | jq -r .id)
vm_id=$(boxy sandbox get $sandbox_id --json | jq -r '.resources[0].id')

# Configure via PowerShell Direct
boxy resource exec $vm_id -- Install-WindowsFeature -Name Web-Server
boxy resource exec $vm_id -- New-Item -Path C:\inetpub\wwwroot\index.html -Value "Hello World"

# Verify
boxy resource exec $vm_id -- Get-WindowsFeature | Where-Object Installed

# Cleanup
boxy sandbox destroy $sandbox_id
```

## Summary

The `Execute` method provides a consistent interface for running commands inside provisioned resources, with provider-specific implementations:

- **Docker**: Uses `docker exec` (already familiar to users)
- **Hyper-V**: Uses PowerShell Direct (no network required) or SSH fallback

This enables users to interact with resources programmatically, making Boxy suitable for:

- Automated testing
- Configuration management
- CI/CD pipelines
- Interactive debugging