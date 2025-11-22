package hyperv

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/crypto"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
)

// Provider implements the provider.Provider interface for Hyper-V
type Provider struct {
	logger    *logrus.Logger
	encryptor *crypto.Encryptor
	ps        *psExecutor
	config    *Config
}

// Config contains Hyper-V provider configuration
type Config struct {
	// VMPath is the directory where VM files are stored
	VMPath string

	// VHDPath is the directory where virtual hard disks are stored
	VHDPath string

	// SwitchName is the virtual switch to connect VMs to
	SwitchName string

	// BaseImagesPath is the directory containing base VHD images
	BaseImagesPath string

	// DefaultGeneration is the VM generation (1 or 2)
	DefaultGeneration int

	// WaitForIPTimeout is how long to wait for VM to get an IP address
	WaitForIPTimeout time.Duration
}

// DefaultConfig returns default Hyper-V configuration
func DefaultConfig() *Config {
	return &Config{
		VMPath:            "C:\\ProgramData\\Boxy\\VMs",
		VHDPath:           "C:\\ProgramData\\Boxy\\VHDs",
		SwitchName:        "Default Switch",
		BaseImagesPath:    "C:\\ProgramData\\Boxy\\BaseImages",
		DefaultGeneration: 2,
		WaitForIPTimeout:  5 * time.Minute,
	}
}

// NewProvider creates a new Hyper-V provider
func NewProvider(logger *logrus.Logger, encryptor *crypto.Encryptor) *Provider {
	return NewProviderWithConfig(logger, encryptor, DefaultConfig())
}

// NewProviderWithConfig creates a new Hyper-V provider with custom config
func NewProviderWithConfig(logger *logrus.Logger, encryptor *crypto.Encryptor, config *Config) *Provider {
	return &Provider{
		logger:    logger,
		encryptor: encryptor,
		ps:        newPSExecutor(logger),
		config:    config,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "hyperv"
}

// Type returns the resource type this provider manages
func (p *Provider) Type() resource.ResourceType {
	return resource.ResourceTypeVM
}

// Provision creates a new VM using Hyper-V
func (p *Provider) Provision(ctx context.Context, spec resource.ResourceSpec) (*resource.Resource, error) {
	p.logger.WithFields(logrus.Fields{
		"image":     spec.Image,
		"cpus":      spec.CPUs,
		"memory_mb": spec.MemoryMB,
		"disk_gb":   spec.DiskGB,
	}).Info("Provisioning Hyper-V VM")

	// Validate resource limits
	if err := validateResourceLimits(spec.CPUs, spec.MemoryMB); err != nil {
		return nil, fmt.Errorf("invalid resource limits: %w", err)
	}

	// Validate image name (prevent path traversal)
	if err := validateImageName(spec.Image); err != nil {
		return nil, fmt.Errorf("invalid image name: %w", err)
	}

	// Generate VM details
	vmID := uuid.New().String()
	vmName := fmt.Sprintf("boxy-%s", vmID[:8])

	// Validate generated VM name (should always pass, but defensive)
	if err := validateVMName(vmName); err != nil {
		return nil, fmt.Errorf("generated invalid VM name: %w", err)
	}

	// Generate credentials
	username := "Administrator"
	password, err := generateSecurePassword(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate password: %w", err)
	}

	// Encrypt password for storage
	encPassword, err := p.encryptor.Encrypt(password)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt password: %w", err)
	}

	// Create differencing disk from base image
	baseImagePath := filepath.Join(p.config.BaseImagesPath, spec.Image+".vhdx")
	vhdPath := filepath.Join(p.config.VHDPath, vmName+".vhdx")

	// Validate paths (defensive - filepath.Join should be safe, but check anyway)
	if err := validatePath(baseImagePath); err != nil {
		return nil, fmt.Errorf("invalid base image path: %w", err)
	}
	if err := validatePath(vhdPath); err != nil {
		return nil, fmt.Errorf("invalid VHD path: %w", err)
	}
	if err := validatePath(p.config.VMPath); err != nil {
		return nil, fmt.Errorf("invalid VM path: %w", err)
	}

	p.logger.WithFields(logrus.Fields{
		"base_image": baseImagePath,
		"vhd_path":   vhdPath,
	}).Debug("Creating differencing disk")

	// Create VHD (differencing disk for fast provisioning)
	// Use single quotes and escaping to prevent injection
	createVHDScript := fmt.Sprintf(`
		$ErrorActionPreference = "Stop"

		# Ensure VHD directory exists
		$vhdDir = Split-Path -Parent '%s'
		if (-not (Test-Path $vhdDir)) {
			New-Item -ItemType Directory -Path $vhdDir -Force | Out-Null
		}

		# Create differencing disk
		New-VHD -Path '%s' -ParentPath '%s' -Differencing | Out-Null
		Write-Output "VHD created successfully"
	`, escapePowerShellString(vhdPath), escapePowerShellString(vhdPath), escapePowerShellString(baseImagePath))

	if _, err := p.ps.exec(ctx, createVHDScript); err != nil {
		return nil, fmt.Errorf("failed to create VHD: %w", err)
	}

	// Create VM
	p.logger.WithField("vm_name", vmName).Debug("Creating VM")

	// Validate switch name
	if err := validateSwitchName(p.config.SwitchName); err != nil {
		return nil, fmt.Errorf("invalid switch name: %w", err)
	}

	// Use single quotes and escaping to prevent injection
	createVMScript := fmt.Sprintf(`
		$ErrorActionPreference = "Stop"

		# Ensure VM directory exists
		$vmDir = '%s'
		if (-not (Test-Path $vmDir)) {
			New-Item -ItemType Directory -Path $vmDir -Force | Out-Null
		}

		# Create VM - use single quotes for all string parameters
		$vm = New-VM -Name '%s' -MemoryStartupBytes %dMB -Generation %d -VHDPath '%s' -Path '%s'

		# Configure VM
		Set-VM -Name '%s' -ProcessorCount %d -AutomaticCheckpointsEnabled $false

		# Connect to network switch
		Connect-VMNetworkAdapter -VMName '%s' -SwitchName '%s'

		Write-Output "VM created successfully"
	`,
		escapePowerShellString(p.config.VMPath),
		escapePowerShellString(vmName),
		spec.MemoryMB,
		p.config.DefaultGeneration,
		escapePowerShellString(vhdPath),
		escapePowerShellString(p.config.VMPath),
		escapePowerShellString(vmName),
		spec.CPUs,
		escapePowerShellString(vmName),
		escapePowerShellString(p.config.SwitchName),
	)

	if _, err := p.ps.exec(ctx, createVMScript); err != nil {
		// Cleanup VHD on failure
		p.cleanupVHD(context.Background(), vhdPath)
		return nil, fmt.Errorf("failed to create VM: %w", err)
	}

	// Start VM
	p.logger.WithField("vm_name", vmName).Debug("Starting VM")

	startVMScript := fmt.Sprintf(`
		$ErrorActionPreference = "Stop"
		Start-VM -Name '%s'
		Write-Output "VM started successfully"
	`, escapePowerShellString(vmName))

	if _, err := p.ps.exec(ctx, startVMScript); err != nil {
		// Cleanup on failure
		p.cleanupVM(context.Background(), vmName, vhdPath)
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	// Wait for VM to get an IP address
	p.logger.WithField("vm_name", vmName).Debug("Waiting for VM to get IP address")

	ipAddress, err := p.waitForIPAddress(ctx, vmName, p.config.WaitForIPTimeout)
	if err != nil {
		p.logger.WithError(err).Warn("Failed to get VM IP address, continuing anyway")
		ipAddress = "" // Will be available later via GetConnectionInfo
	}

	p.logger.WithFields(logrus.Fields{
		"vm_name": vmName,
		"vm_id":   vmID,
		"ip":      ipAddress,
	}).Info("Hyper-V VM provisioned successfully")

	// Build resource
	res := &resource.Resource{
		ID:           uuid.New().String(),
		Type:         resource.ResourceTypeVM,
		ProviderType: "hyperv",
		ProviderID:   vmName, // Use VM name as provider ID
		State:        resource.StateReady,
		Metadata: map[string]interface{}{
			"vm_name":       vmName,
			"vm_id":         vmID,
			"ip_address":    ipAddress,
			"image":         spec.Image,
			"cpus":          spec.CPUs,
			"memory_mb":     spec.MemoryMB,
			"disk_gb":       spec.DiskGB,
			"vhd_path":      vhdPath,
			"username":      username,
			"password_enc":  encPassword,
			"generation":    p.config.DefaultGeneration,
		},
	}

	return res, nil
}

// Destroy destroys a VM
func (p *Provider) Destroy(ctx context.Context, res *resource.Resource) error {
	vmName := res.ProviderID

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		return fmt.Errorf("invalid VM name: %w", err)
	}

	p.logger.WithField("vm_name", vmName).Info("Destroying Hyper-V VM")

	// Get VHD path from metadata if available
	vhdPath, _ := res.Metadata["vhd_path"].(string)

	// Stop VM if running
	stopScript := fmt.Sprintf(`
		$ErrorActionPreference = "Continue"
		$vm = Get-VM -Name '%s' -ErrorAction SilentlyContinue
		if ($vm -and $vm.State -ne "Off") {
			Stop-VM -Name '%s' -Force -TurnOff
		}
		Write-Output "VM stopped"
	`, escapePowerShellString(vmName), escapePowerShellString(vmName))

	if _, err := p.ps.exec(ctx, stopScript); err != nil {
		p.logger.WithError(err).Warn("Failed to stop VM, continuing with removal")
	}

	// Remove VM
	removeScript := fmt.Sprintf(`
		$ErrorActionPreference = "Continue"
		$vm = Get-VM -Name '%s' -ErrorAction SilentlyContinue
		if ($vm) {
			Remove-VM -Name '%s' -Force
			Write-Output "VM removed"
		} else {
			Write-Output "VM not found, skipping removal"
		}
	`, escapePowerShellString(vmName), escapePowerShellString(vmName))

	if _, err := p.ps.exec(ctx, removeScript); err != nil {
		p.logger.WithError(err).Warn("Failed to remove VM")
	}

	// Remove VHD if path is known
	if vhdPath != "" {
		if err := p.cleanupVHD(ctx, vhdPath); err != nil {
			p.logger.WithError(err).Warn("Failed to remove VHD")
		}
	}

	p.logger.WithField("vm_name", vmName).Info("Hyper-V VM destroyed")
	return nil
}

// GetStatus returns the current status of a VM
func (p *Provider) GetStatus(ctx context.Context, res *resource.Resource) (*resource.ResourceStatus, error) {
	vmName := res.ProviderID

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		return nil, fmt.Errorf("invalid VM name: %w", err)
	}

	script := fmt.Sprintf(`
		$ErrorActionPreference = "Stop"
		$vm = Get-VM -Name '%s'

		$vm | Select-Object Name, State, CPUUsage, MemoryAssigned, Uptime, Status, CreationTime | ConvertTo-Json -Compress
	`, escapePowerShellString(vmName))

	var info vmInfo
	if err := p.ps.execJSON(ctx, script, &info); err != nil {
		return &resource.ResourceStatus{
			State:   resource.StateError,
			Healthy: false,
			Message: fmt.Sprintf("Failed to get VM status: %v", err),
		}, nil
	}

	// Parse uptime
	uptime, _ := parseHyperVUptime(info.Uptime)

	// Map Hyper-V state to resource state
	state := resource.StateReady
	healthy := info.State == "Running"

	if info.State == "Off" || info.State == "Stopped" || info.State == "Paused" {
		// VM is not running but not in error state
		state = resource.StateReady
		healthy = false
	}

	return &resource.ResourceStatus{
		State:      state,
		Healthy:    healthy,
		Message:    fmt.Sprintf("VM state: %s, Status: %s", info.State, info.Status),
		LastCheck:  time.Now(),
		Uptime:     uptime,
		CPUUsage:   float64(info.CPUUsage),
		MemoryUsed: uint64(info.MemoryAssigned),
	}, nil
}

// GetConnectionInfo returns connection details for a VM
func (p *Provider) GetConnectionInfo(ctx context.Context, res *resource.Resource) (*resource.ConnectionInfo, error) {
	vmName := res.ProviderID

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		return nil, fmt.Errorf("invalid VM name: %w", err)
	}

	// Get IP address from network adapter
	ipAddress, err := p.getVMIPAddress(ctx, vmName)
	if err != nil {
		// Try to get from metadata as fallback
		if savedIP, ok := res.Metadata["ip_address"].(string); ok && savedIP != "" {
			ipAddress = savedIP
		} else {
			return nil, fmt.Errorf("failed to get VM IP address: %w", err)
		}
	}

	// Get credentials from metadata
	username, _ := res.Metadata["username"].(string)
	if username == "" {
		username = "Administrator" // Default
	}

	encPassword, _ := res.Metadata["password_enc"].(string)
	if encPassword == "" {
		return nil, fmt.Errorf("password not found in resource metadata")
	}

	// Decrypt password
	decPassword, err := p.encryptor.Decrypt(encPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt password: %w", err)
	}

	return &resource.ConnectionInfo{
		Type:     "rdp",
		Host:     ipAddress,
		Port:     3389,
		Username: username,
		Password: string(decPassword),
		ExtraFields: map[string]interface{}{
			"vm_name": vmName,
		},
	}, nil
}

// Exec runs a command inside the VM using PowerShell Direct
func (p *Provider) Exec(ctx context.Context, res *resource.Resource, cmd []string) (*provider_pkg.ExecResult, error) {
	vmName := res.ProviderID

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		return nil, fmt.Errorf("invalid VM name: %w", err)
	}

	// Validate command is not empty
	if len(cmd) == 0 {
		return nil, fmt.Errorf("command cannot be empty")
	}

	p.logger.WithFields(logrus.Fields{
		"vm_name": vmName,
		"cmd":     cmd,
	}).Debug("Executing command via PowerShell Direct")

	// Get credentials
	username, _ := res.Metadata["username"].(string)
	if username == "" {
		username = "Administrator"
	}

	encPassword, _ := res.Metadata["password_enc"].(string)
	if encPassword == "" {
		return nil, fmt.Errorf("password not found in resource metadata")
	}

	decPassword, err := p.encryptor.Decrypt(encPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt password: %w", err)
	}

	// Use PowerShell Direct (Invoke-Command -VMName) with ArgumentList
	// This passes the command as DATA via -ArgumentList, not embedded code
	// This prevents ScriptBlock breakout attacks (e.g., cmd with "}" chars)
	//
	// Security approach:
	// 1. Command array passed as argument (not embedded in ScriptBlock)
	// 2. ScriptBlock receives it as pure data via param()
	// 3. Call operator (&) executes command inside VM, not on host

	// Build the command array in PowerShell array syntax
	// Each element is single-quoted and escaped
	var cmdElements []string
	for _, arg := range cmd {
		cmdElements = append(cmdElements, fmt.Sprintf("'%s'", escapePowerShellString(arg)))
	}
	cmdArrayStr := strings.Join(cmdElements, ",")

	script := fmt.Sprintf(`
		$ErrorActionPreference = "Stop"

		$password = ConvertTo-SecureString '%s' -AsPlainText -Force
		$cred = New-Object System.Management.Automation.PSCredential('%s', $password)

		# Command array passed as data, not code
		$cmdArray = @(%s)

		$result = Invoke-Command -VMName '%s' -Credential $cred -ScriptBlock {
			param([string[]]$command)

			# Execute command inside VM using call operator
			# First element is executable, rest are arguments
			if ($command.Length -eq 1) {
				& $command[0] 2>&1
			} else {
				& $command[0] $command[1..($command.Length-1)] 2>&1
			}
		} -ArgumentList (,$cmdArray) -ErrorVariable execError

		if ($execError) {
			throw $execError
		}

		$result
	`, escapePowerShellString(string(decPassword)), escapePowerShellString(username), cmdArrayStr, escapePowerShellString(vmName))

	output, err := p.ps.exec(ctx, script)

	result := &provider_pkg.ExecResult{
		ExitCode: 0,
		Stdout:   output,
		Stderr:   "",
	}

	if err != nil {
		result.ExitCode = 1
		result.Error = err
		result.Stderr = err.Error()
	}

	p.logger.WithFields(logrus.Fields{
		"vm_name":   vmName,
		"exit_code": result.ExitCode,
	}).Debug("Command executed via PowerShell Direct")

	return result, nil
}

// Update modifies a VM (power state, snapshots, resource limits)
func (p *Provider) Update(ctx context.Context, res *resource.Resource, updates provider_pkg.ResourceUpdate) error {
	vmName := res.ProviderID

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		return fmt.Errorf("invalid VM name: %w", err)
	}

	p.logger.WithFields(logrus.Fields{
		"vm_name": vmName,
		"updates": fmt.Sprintf("%+v", updates),
	}).Info("Updating Hyper-V VM")

	// Handle power state changes
	if updates.PowerState != nil {
		if err := p.updatePowerState(ctx, vmName, *updates.PowerState); err != nil {
			return fmt.Errorf("failed to update power state: %w", err)
		}
	}

	// Handle snapshot operations
	if updates.Snapshot != nil {
		if err := p.updateSnapshot(ctx, vmName, updates.Snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot: %w", err)
		}
	}

	// Handle resource limit changes
	if updates.Resources != nil {
		if err := p.updateResources(ctx, vmName, updates.Resources); err != nil {
			return fmt.Errorf("failed to update resources: %w", err)
		}
	}

	p.logger.WithField("vm_name", vmName).Info("Hyper-V VM updated successfully")
	return nil
}

// HealthCheck checks if Hyper-V is accessible
func (p *Provider) HealthCheck(ctx context.Context) error {
	p.logger.Debug("Performing Hyper-V health check")

	script := `
		$ErrorActionPreference = "Stop"

		# Check if Hyper-V management service is running
		$service = Get-Service -Name vmms -ErrorAction SilentlyContinue
		if (-not $service) {
			throw "Hyper-V Virtual Machine Management service not found"
		}

		if ($service.Status -ne "Running") {
			throw "Hyper-V Virtual Machine Management service is not running: $($service.Status)"
		}

		# Try to get VMHost to verify we can query Hyper-V
		$vmHost = Get-VMHost

		Write-Output "Hyper-V is healthy"
	`

	if _, err := p.ps.exec(ctx, script); err != nil {
		return fmt.Errorf("hyper-v health check failed: %w", err)
	}

	p.logger.Debug("Hyper-V health check passed")
	return nil
}

// Helper methods

func (p *Provider) cleanupVM(ctx context.Context, vmName, vhdPath string) {
	// Try to stop and remove VM
	script := fmt.Sprintf(`
		$ErrorActionPreference = "Continue"
		Stop-VM -Name '%s' -Force -TurnOff -ErrorAction SilentlyContinue
		Remove-VM -Name '%s' -Force -ErrorAction SilentlyContinue
	`, escapePowerShellString(vmName), escapePowerShellString(vmName))

	p.ps.exec(ctx, script)

	// Cleanup VHD
	if vhdPath != "" {
		p.cleanupVHD(ctx, vhdPath)
	}
}

func (p *Provider) cleanupVHD(ctx context.Context, vhdPath string) error {
	script := fmt.Sprintf(`
		$ErrorActionPreference = "Continue"
		if (Test-Path '%s') {
			Remove-Item -Path '%s' -Force
		}
	`, escapePowerShellString(vhdPath), escapePowerShellString(vhdPath))

	_, err := p.ps.exec(ctx, script)
	return err
}

func (p *Provider) waitForIPAddress(ctx context.Context, vmName string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for VM IP address")
		case <-ticker.C:
			ip, err := p.getVMIPAddress(ctx, vmName)
			if err == nil && ip != "" {
				return ip, nil
			}
		}
	}
}

func (p *Provider) getVMIPAddress(ctx context.Context, vmName string) (string, error) {
	script := fmt.Sprintf(`
		$ErrorActionPreference = "Stop"
		$adapter = Get-VMNetworkAdapter -VMName '%s'
		$ips = $adapter.IPAddresses | Where-Object { $_ -match '^\d+\.\d+\.\d+\.\d+$' }
		if ($ips) {
			$ips[0]
		} else {
			throw "No IPv4 address found"
		}
	`, escapePowerShellString(vmName))

	ip, err := p.ps.exec(ctx, script)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(ip), nil
}

func (p *Provider) updatePowerState(ctx context.Context, vmName string, state provider_pkg.PowerState) error {
	// Validate VM name (defensive check)
	if err := validateVMName(vmName); err != nil {
		return fmt.Errorf("invalid VM name: %w", err)
	}

	var script string

	switch state {
	case provider_pkg.PowerStateRunning:
		script = fmt.Sprintf(`Start-VM -Name '%s'`, escapePowerShellString(vmName))
		p.logger.WithField("vm_name", vmName).Info("Starting VM")

	case provider_pkg.PowerStateStopped:
		script = fmt.Sprintf(`Stop-VM -Name '%s' -Force`, escapePowerShellString(vmName))
		p.logger.WithField("vm_name", vmName).Info("Stopping VM")

	case provider_pkg.PowerStateReset:
		script = fmt.Sprintf(`Restart-VM -Name '%s' -Force`, escapePowerShellString(vmName))
		p.logger.WithField("vm_name", vmName).Info("Restarting VM")

	case provider_pkg.PowerStatePaused:
		script = fmt.Sprintf(`Suspend-VM -Name '%s'`, escapePowerShellString(vmName))
		p.logger.WithField("vm_name", vmName).Info("Pausing VM")

	default:
		return fmt.Errorf("unsupported power state: %s", state)
	}

	if _, err := p.ps.exec(ctx, script); err != nil {
		return err
	}

	return nil
}

func (p *Provider) updateSnapshot(ctx context.Context, vmName string, snapshot *provider_pkg.SnapshotOp) error {
	// Validate inputs
	if err := validateVMName(vmName); err != nil {
		return fmt.Errorf("invalid VM name: %w", err)
	}
	if err := validateSnapshotName(snapshot.Name); err != nil {
		return fmt.Errorf("invalid snapshot name: %w", err)
	}
	if err := validateSnapshotOperation(snapshot.Operation); err != nil {
		return err
	}

	switch snapshot.Operation {
	case "create":
		script := fmt.Sprintf(`Checkpoint-VM -Name '%s' -SnapshotName '%s'`,
			escapePowerShellString(vmName), escapePowerShellString(snapshot.Name))
		p.logger.WithFields(logrus.Fields{
			"vm_name":       vmName,
			"snapshot_name": snapshot.Name,
		}).Info("Creating VM snapshot")

		if _, err := p.ps.exec(ctx, script); err != nil {
			return err
		}

	case "restore":
		script := fmt.Sprintf(`Restore-VMCheckpoint -VMName '%s' -Name '%s' -Confirm:$false`,
			escapePowerShellString(vmName), escapePowerShellString(snapshot.Name))
		p.logger.WithFields(logrus.Fields{
			"vm_name":       vmName,
			"snapshot_name": snapshot.Name,
		}).Info("Restoring VM snapshot")

		if _, err := p.ps.exec(ctx, script); err != nil {
			return err
		}

	case "delete":
		script := fmt.Sprintf(`Remove-VMCheckpoint -VMName '%s' -Name '%s' -Confirm:$false`,
			escapePowerShellString(vmName), escapePowerShellString(snapshot.Name))
		p.logger.WithFields(logrus.Fields{
			"vm_name":       vmName,
			"snapshot_name": snapshot.Name,
		}).Info("Deleting VM snapshot")

		if _, err := p.ps.exec(ctx, script); err != nil {
			return err
		}

	default:
		return fmt.Errorf("unsupported snapshot operation: %s", snapshot.Operation)
	}

	return nil
}

func (p *Provider) updateResources(ctx context.Context, vmName string, resources *provider_pkg.ResourceLimits) error {
	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		return fmt.Errorf("invalid VM name: %w", err)
	}

	var updates []string

	if resources.CPUs != nil {
		// Validate CPU count
		if err := validateResourceLimits(*resources.CPUs, 1024); err != nil {
			return fmt.Errorf("invalid CPU count: %w", err)
		}
		updates = append(updates, fmt.Sprintf("-ProcessorCount %d", *resources.CPUs))
		p.logger.WithFields(logrus.Fields{
			"vm_name": vmName,
			"cpus":    *resources.CPUs,
		}).Info("Updating VM CPU count")
	}

	if resources.MemoryMB != nil {
		// Validate memory
		if err := validateResourceLimits(1, *resources.MemoryMB); err != nil {
			return fmt.Errorf("invalid memory size: %w", err)
		}
		// Convert MB to bytes for Set-VM
		memoryBytes := int64(*resources.MemoryMB) * 1024 * 1024
		updates = append(updates, fmt.Sprintf("-MemoryStartupBytes %d", memoryBytes))
		p.logger.WithFields(logrus.Fields{
			"vm_name":   vmName,
			"memory_mb": *resources.MemoryMB,
		}).Info("Updating VM memory")
	}

	if len(updates) > 0 {
		script := fmt.Sprintf(`Set-VM -Name '%s' %s`, escapePowerShellString(vmName), strings.Join(updates, " "))
		if _, err := p.ps.exec(ctx, script); err != nil {
			return err
		}
	}

	return nil
}

// generateSecurePassword generates a cryptographically secure random password
func generateSecurePassword(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	// Use base64 encoding for readable password
	password := base64.URLEncoding.EncodeToString(bytes)

	// Trim to desired length and ensure it has required character types
	if len(password) > length {
		password = password[:length]
	}

	// Add special char and number to meet Windows complexity requirements
	password = "P@ss" + password[:length-4]

	return password, nil
}

// parseHyperVUptime parses Hyper-V uptime string (format: "00:12:34:56.1234567")
func parseHyperVUptime(uptimeStr string) (time.Duration, error) {
	if uptimeStr == "" {
		return 0, nil
	}

	// Parse format: days:hours:minutes:seconds.fraction
	parts := strings.Split(uptimeStr, ":")
	if len(parts) < 4 {
		return 0, fmt.Errorf("invalid uptime format: %s", uptimeStr)
	}

	var days, hours, minutes int
	var seconds float64

	fmt.Sscanf(parts[0], "%d", &days)
	fmt.Sscanf(parts[1], "%d", &hours)
	fmt.Sscanf(parts[2], "%d", &minutes)
	fmt.Sscanf(parts[3], "%f", &seconds)

	duration := time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second))

	return duration, nil
}
