# 05: Base Image Validation

---

## Metadata

```yaml
feature: "Base Image Validation"
slug: "base-image-validation"
status: "not-started"
priority: "medium"
type: "feature"
effort: "small"
depends_on: []
enables: ["image-quality", "early-failure-detection"]
testing: ["unit", "integration"]
breaking_change: false
week: "3-4"
related_docs:
  - "06-pool-cli-commands.md"
```

---

## Overview

Validate base images meet Boxy's requirements before using in production pools. Catches configuration issues early rather than at allocation time.

**Minimal Contract**: What Boxy requires from a base image:
1. **Ability to execute commands** - SSH, WinRM, or docker exec
2. **Network connectivity** - Reachable from Boxy server
3. **Provider lifecycle support** - Start, stop, destroy

**That's it!** Everything else is user's responsibility.

---

## Configuration

### Validation Checks

**Add to pool config:**

```yaml
pools:
  - name: win-test-vms
    type: vm
    backend: hyperv
    image:
      source: windows-11-base.vhdx

    validation:
      required:
        - name: "PowerShell Available"
          command: powershell
          args: ["-Command", "Write-Host 'PowerShell OK'"]
          timeout: 10s

        - name: "Admin Rights"
          command: powershell
          args:
            - "-Command"
            - |
              $isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
              if (-not $isAdmin) { exit 1 }
              Write-Host "Admin rights confirmed"
          timeout: 5s

        - name: "WinRM Enabled"
          command: powershell
          args: ["-Command", "Test-WSMan -ErrorAction Stop"]
          timeout: 10s

      optional:
        - name: "Hyper-V Guest Services"
          command: powershell
          args: ["-Command", "Get-Service vmicguestinterface"]
          timeout: 5s

        - name: "Network Connectivity"
          command: powershell
          args: ["-Command", "Test-NetConnection google.com -Port 80"]
          timeout: 15s
```

**For containers:**

```yaml
pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04

    validation:
      required:
        - name: "Shell Available"
          command: sh
          args: ["-c", "echo 'Shell OK'"]

        - name: "Basic Commands"
          command: sh
          args: ["-c", "which curl && which wget"]

      optional:
        - name: "Python Installed"
          command: python3
          args: ["--version"]
```

---

## CLI Command

### Validate Image

```bash
# Validate all pools
boxy admin validate-images

# Validate specific pool
boxy admin validate-image --pool win-test-vms

# Verbose output
boxy admin validate-image --pool win-test-vms --verbose

# Output:
Validating pool 'win-test-vms'...

Provisioning test resource... ✓ (15.2s)

Running required checks:
  ✓ PowerShell Available (0.5s)
  ✓ Admin Rights (0.3s)
  ✓ WinRM Enabled (1.2s)

Running optional checks:
  ✓ Hyper-V Guest Services (0.4s)
  ⚠ Network Connectivity (timeout) - This is optional

Cleaning up test resource... ✓ (2.1s)

✅ Image is valid for use with Boxy!

Summary:
  Required: 3/3 passed
  Optional: 1/2 passed
  Total time: 19.7s
```

---

## Implementation

### Task 5.1: Validation Config Model

**File**: `internal/core/pool/validation.go`

```go
package pool

import "time"

type ValidationConfig struct {
    Required []ValidationCheck `yaml:"required"`
    Optional []ValidationCheck `yaml:"optional"`
}

type ValidationCheck struct {
    Name    string        `yaml:"name"`
    Command string        `yaml:"command"`
    Args    []string      `yaml:"args"`
    Timeout time.Duration `yaml:"timeout"`
}

type ValidationResult struct {
    CheckName string
    Passed    bool
    Output    string
    Error     error
    Duration  time.Duration
}
```

---

### Task 5.2: Validator Implementation

**File**: `internal/core/pool/validator.go`

```go
package pool

type Validator struct {
    provider provider.Provider
    logger   *logrus.Logger
}

func NewValidator(provider provider.Provider, logger *logrus.Logger) *Validator {
    return &Validator{
        provider: provider,
        logger:   logger,
    }
}

func (v *Validator) ValidateImage(ctx context.Context, poolCfg *PoolConfig) (*ValidationReport, error) {
    v.logger.WithField("pool", poolCfg.Name).Info("Starting image validation")

    report := &ValidationReport{
        PoolName:  poolCfg.Name,
        StartTime: time.Now(),
    }

    // 1. Provision temporary resource
    v.logger.Info("Provisioning test resource...")
    spec := poolCfg.ToResourceSpec()
    res, err := v.provider.Provision(ctx, spec)
    if err != nil {
        return nil, fmt.Errorf("failed to provision test resource: %w", err)
    }
    defer func() {
        v.logger.Info("Cleaning up test resource...")
        v.provider.Destroy(context.Background(), res)
    }()

    report.ProvisionDuration = time.Since(report.StartTime)

    // 2. Wait for resource to be ready
    if err := v.waitForReady(ctx, res); err != nil {
        return nil, fmt.Errorf("resource not ready: %w", err)
    }

    // 3. Run required checks
    v.logger.Info("Running required checks...")
    for _, check := range poolCfg.Validation.Required {
        result := v.runCheck(ctx, res, check)
        report.RequiredResults = append(report.RequiredResults, result)

        if !result.Passed {
            report.Failed = true
            report.FailReason = fmt.Sprintf("Required check failed: %s", check.Name)
        }
    }

    // 4. Run optional checks (don't fail on errors)
    v.logger.Info("Running optional checks...")
    for _, check := range poolCfg.Validation.Optional {
        result := v.runCheck(ctx, res, check)
        report.OptionalResults = append(report.OptionalResults, result)
        // Optional checks don't cause validation to fail
    }

    report.EndTime = time.Now()
    report.TotalDuration = report.EndTime.Sub(report.StartTime)

    return report, nil
}

func (v *Validator) runCheck(ctx context.Context, res *resource.Resource, check ValidationCheck) ValidationResult {
    result := ValidationResult{
        CheckName: check.Name,
    }

    start := time.Now()

    // Create timeout context
    checkCtx, cancel := context.WithTimeout(ctx, check.Timeout)
    defer cancel()

    // Execute command
    execResult, err := v.provider.Exec(checkCtx, res, append([]string{check.Command}, check.Args...))
    result.Duration = time.Since(start)

    if err != nil {
        result.Passed = false
        result.Error = err
        v.logger.WithField("check", check.Name).WithError(err).Warn("Check failed")
        return result
    }

    if execResult.ExitCode == 0 {
        result.Passed = true
        result.Output = execResult.Stdout
        v.logger.WithField("check", check.Name).Info("Check passed")
    } else {
        result.Passed = false
        result.Output = execResult.Stderr
        result.Error = fmt.Errorf("exit code %d", execResult.ExitCode)
        v.logger.WithField("check", check.Name).Warn("Check failed")
    }

    return result
}

func (v *Validator) waitForReady(ctx context.Context, res *resource.Resource) error {
    // Poll until resource is ready or timeout
    timeout := time.After(5 * time.Minute)
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-timeout:
            return errors.New("timeout waiting for resource to be ready")
        case <-ticker.C:
            status, err := v.provider.GetStatus(ctx, res)
            if err != nil {
                continue
            }
            if status.State == "running" {
                // Try to get connection info
                if _, err := v.provider.GetConnectionInfo(ctx, res); err == nil {
                    return nil // Ready!
                }
            }
        }
    }
}

type ValidationReport struct {
    PoolName          string
    StartTime         time.Time
    EndTime           time.Time
    TotalDuration     time.Duration
    ProvisionDuration time.Duration
    RequiredResults   []ValidationResult
    OptionalResults   []ValidationResult
    Failed            bool
    FailReason        string
}

func (r *ValidationReport) Print() {
    fmt.Printf("\nValidation Report for '%s'\n", r.PoolName)
    fmt.Printf("=====================================\n\n")

    fmt.Printf("Provisioning: ✓ (%.1fs)\n\n", r.ProvisionDuration.Seconds())

    fmt.Printf("Required Checks:\n")
    for _, result := range r.RequiredResults {
        status := "✓"
        if !result.Passed {
            status = "✗"
        }
        fmt.Printf("  %s %s (%.1fs)\n", status, result.CheckName, result.Duration.Seconds())
        if !result.Passed && result.Error != nil {
            fmt.Printf("    Error: %v\n", result.Error)
        }
    }

    fmt.Printf("\nOptional Checks:\n")
    for _, result := range r.OptionalResults {
        status := "✓"
        if !result.Passed {
            status = "⚠"
        }
        fmt.Printf("  %s %s (%.1fs)\n", status, result.CheckName, result.Duration.Seconds())
    }

    fmt.Printf("\nSummary:\n")
    requiredPassed := 0
    for _, r := range r.RequiredResults {
        if r.Passed {
            requiredPassed++
        }
    }
    optionalPassed := 0
    for _, r := range r.OptionalResults {
        if r.Passed {
            optionalPassed++
        }
    }

    fmt.Printf("  Required: %d/%d passed\n", requiredPassed, len(r.RequiredResults))
    fmt.Printf("  Optional: %d/%d passed\n", optionalPassed, len(r.OptionalResults))
    fmt.Printf("  Total time: %.1fs\n\n", r.TotalDuration.Seconds())

    if r.Failed {
        fmt.Printf("❌ Image validation FAILED: %s\n", r.FailReason)
    } else {
        fmt.Printf("✅ Image is valid for use with Boxy!\n")
    }
}
```

---

### Task 5.3: CLI Command

**File**: `cmd/boxy/commands/admin_validate.go`

```go
package commands

func adminValidateImageCommand() *cobra.Command {
    var poolName string
    var verbose bool

    cmd := &cobra.Command{
        Use:   "validate-image",
        Short: "Validate base image for a pool",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Load config
            config, err := config.LoadConfig("")
            if err != nil {
                return err
            }

            // Find pool config
            var poolCfg *pool.PoolConfig
            for _, p := range config.Pools {
                if p.Name == poolName {
                    poolCfg = p
                    break
                }
            }
            if poolCfg == nil {
                return fmt.Errorf("pool not found: %s", poolName)
            }

            // Get provider
            provider, err := getProvider(poolCfg.Backend)
            if err != nil {
                return err
            }

            // Create validator
            validator := pool.NewValidator(provider, logger)

            // Run validation
            report, err := validator.ValidateImage(cmd.Context(), poolCfg)
            if err != nil {
                return err
            }

            // Print report
            report.Print()

            if report.Failed {
                os.Exit(1)
            }

            return nil
        },
    }

    cmd.Flags().StringVarP(&poolName, "pool", "p", "", "Pool name to validate (required)")
    cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
    cmd.MarkFlagRequired("pool")

    return cmd
}
```

---

## Testing

### Unit Tests

```go
// internal/core/pool/validator_test.go
func TestValidator_RunCheck_Success(t *testing.T) {
    mockProvider := &mockProvider{}
    validator := NewValidator(mockProvider, logger)

    check := ValidationCheck{
        Name:    "Test Check",
        Command: "echo",
        Args:    []string{"hello"},
        Timeout: 5 * time.Second,
    }

    res := &resource.Resource{ID: "test-res"}
    result := validator.runCheck(context.Background(), res, check)

    assert.True(t, result.Passed)
    assert.Contains(t, result.Output, "hello")
}

func TestValidator_RunCheck_Timeout(t *testing.T) {
    mockProvider := &mockProvider{
        execDelay: 10 * time.Second, // Simulate slow command
    }
    validator := NewValidator(mockProvider, logger)

    check := ValidationCheck{
        Name:    "Slow Check",
        Command: "sleep",
        Args:    []string{"100"},
        Timeout: 1 * time.Second, // Short timeout
    }

    res := &resource.Resource{ID: "test-res"}
    result := validator.runCheck(context.Background(), res, check)

    assert.False(t, result.Passed)
    assert.Error(t, result.Error)
}
```

### Integration Tests

```go
// tests/integration/validation_test.go
func TestIntegration_ValidateDockerImage(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    poolCfg := &pool.PoolConfig{
        Name:    "test-pool",
        Type:    resource.ResourceTypeContainer,
        Backend: "docker",
        Image:   "ubuntu:22.04",
        Validation: pool.ValidationConfig{
            Required: []pool.ValidationCheck{
                {
                    Name:    "Shell Available",
                    Command: "sh",
                    Args:    []string{"-c", "echo 'OK'"},
                    Timeout: 5 * time.Second,
                },
            },
        },
    }

    provider := docker.NewProvider(logger, encryptor)
    validator := pool.NewValidator(provider, logger)

    report, err := validator.ValidateImage(context.Background(), poolCfg)
    assert.NoError(t, err)
    assert.False(t, report.Failed)
    assert.Len(t, report.RequiredResults, 1)
    assert.True(t, report.RequiredResults[0].Passed)
}
```

---

## Success Criteria

- ✅ ValidationConfig model implemented
- ✅ Validator implementation complete
- ✅ CLI command `boxy admin validate-image` works
- ✅ Required checks cause validation to fail
- ✅ Optional checks warn but don't fail
- ✅ Test resource cleaned up after validation
- ✅ Clear, actionable error messages
- ✅ Integration tests pass with Docker

---

## User Impact

### Benefits

- **Early detection**: Catch image issues before production use
- **Clear feedback**: Know exactly what's wrong with an image
- **Documentation**: Validation config documents image requirements
- **Confidence**: Verify images work before creating pools

### Example Workflow

```bash
# 1. Create pool config with validation checks
cat > pool.yaml <<EOF
name: win-test-vms
type: vm
backend: hyperv
image: windows-11-base.vhdx
validation:
  required:
    - name: "WinRM Enabled"
      command: powershell
      args: ["-Command", "Test-WSMan"]
EOF

# 2. Validate image before adding to production
boxy admin validate-image --pool win-test-vms

# 3. If validation passes, add to config
# If validation fails, fix image and retry
```

---

## Related Documents

- [06: Pool CLI Commands](06-pool-cli-commands.md) - Pool management
- [02: Preheating & Recycling](02-preheating-recycling.md) - Resource lifecycle

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: None
**Blocking**: None (quality-of-life feature)
