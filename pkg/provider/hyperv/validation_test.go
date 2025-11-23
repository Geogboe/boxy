package hyperv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateVMName tests VM name validation
func TestValidateVMName(t *testing.T) {
	tests := []struct {
		name    string
		vmName  string
		wantErr bool
	}{
		{"valid simple", "boxy-vm-01", false},
		{"valid with numbers", "vm123", false},
		{"valid with underscores", "my_vm", false},
		{"valid with hyphens", "my-vm", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 101)), true},
		{"semicolon injection", "vm; Remove-VM", true},
		{"quote injection", `vm"; Remove-VM -Name "other`, true},
		{"backtick injection", "vm`; Remove-VM", true},
		{"dollar sign", "vm$var", true},
		{"pipe injection", "vm | Remove-VM", true},
		{"ampersand", "vm & Remove-VM", true},
		{"reserved CON", "CON", true},
		{"reserved PRN", "PRN", true},
		{"reserved NUL", "NUL", true},
		{"reserved LPT1", "LPT1", true},
		{"spaces", "my vm", true},
		{"special chars", "vm@host", true},
		{"path traversal", "../vm", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVMName(tt.vmName)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for VM name: %s", tt.vmName)
			} else {
				assert.NoError(t, err, "Expected no error for VM name: %s", tt.vmName)
			}
		})
	}
}

// TestValidateSnapshotName tests snapshot name validation
func TestValidateSnapshotName(t *testing.T) {
	tests := []struct {
		name         string
		snapshotName string
		wantErr      bool
	}{
		{"valid simple", "snapshot1", false},
		{"valid with spaces", "Before Update", false},
		{"valid with hyphen", "pre-deployment", false},
		{"valid with underscore", "snapshot_001", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 101)), true},
		{"semicolon injection", "snap; Remove-VM", true},
		{"quote injection", `snap"; Remove-VM`, true},
		{"backtick", "snap`", true},
		{"dollar sign", "snap$var", true},
		{"pipe", "snap | cmd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSnapshotName(tt.snapshotName)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for snapshot name: %s", tt.snapshotName)
			} else {
				assert.NoError(t, err, "Expected no error for snapshot name: %s", tt.snapshotName)
			}
		})
	}
}

// TestValidatePath tests path validation
func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid Windows path", "C:\\VMs\\vm1.vhdx", false},
		{"valid Linux path", "/var/lib/vms/vm1.vhdx", false},
		{"empty", "", true},
		{"relative path", "vms/vm1.vhdx", true},
		{"semicolon injection", "C:\\VMs; Remove-Item", true},
		{"backtick injection", "C:\\VMs`; cmd", true},
		{"dollar sign", "C:\\VMs\\$var", true},
		{"pipe", "C:\\VMs | cmd", true},
		{"ampersand", "C:\\VMs & cmd", true},
		{"less than", "C:\\VMs<file", true},
		{"greater than", "C:\\VMs>file", true},
		{"caret", "C:\\VMs^cmd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for path: %s", tt.path)
			} else {
				assert.NoError(t, err, "Expected no error for path: %s", tt.path)
			}
		})
	}
}

// TestValidateSwitchName tests switch name validation
func TestValidateSwitchName(t *testing.T) {
	tests := []struct {
		name       string
		switchName string
		wantErr    bool
	}{
		{"valid simple", "Default Switch", false},
		{"valid no spaces", "ExternalSwitch", false},
		{"valid with hyphen", "External-Switch", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 101)), true},
		{"semicolon", "Switch; Remove-VM", true},
		{"backtick", "Switch`", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSwitchName(tt.switchName)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for switch name: %s", tt.switchName)
			} else {
				assert.NoError(t, err, "Expected no error for switch name: %s", tt.switchName)
			}
		})
	}
}

// TestValidateResourceLimits tests resource limit validation
func TestValidateResourceLimits(t *testing.T) {
	tests := []struct {
		name     string
		cpus     int
		memoryMB int
		wantErr  bool
	}{
		{"valid minimal", 1, 512, false},
		{"valid typical", 4, 4096, false},
		{"valid maximum", 64, 1048576, false},
		{"zero CPUs", 0, 2048, true},
		{"negative CPUs", -1, 2048, true},
		{"too many CPUs", 65, 2048, true},
		{"too little memory", 2, 256, true},
		{"too much memory", 2, 2000000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResourceLimits(tt.cpus, tt.memoryMB)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateSnapshotOperation tests snapshot operation validation
func TestValidateSnapshotOperation(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		wantErr   bool
	}{
		{"valid create", "create", false},
		{"valid restore", "restore", false},
		{"valid delete", "delete", false},
		{"invalid operation", "invalid", true},
		{"injection attempt", "create; Remove-VM", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSnapshotOperation(tt.operation)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateImageName tests image name validation
func TestValidateImageName(t *testing.T) {
	tests := []struct {
		name      string
		imageName string
		wantErr   bool
	}{
		{"valid simple", "windows-server-2022", false},
		{"valid with version", "ubuntu-22.04", false},
		{"valid with dots", "centos-8.5.2111", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 101)), true},
		{"path traversal", "../../../etc/passwd", true},
		{"path traversal 2", "..\\..\\Windows\\System32", true},
		{"semicolon", "image; Remove-VM", true},
		{"spaces", "my image", true},
		{"special chars", "image@version", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateImageName(tt.imageName)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for image name: %s", tt.imageName)
			} else {
				assert.NoError(t, err, "Expected no error for image name: %s", tt.imageName)
			}
		})
	}
}

// TestEscapePowerShellString tests PowerShell string escaping
func TestEscapePowerShellString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no escaping needed", "simple-vm", "simple-vm"},
		{"single quote", "it's", "it''s"},
		{"multiple quotes", "I'm 'quoted'", "I''m ''quoted''"},
		{"empty string", "", ""},
		{"only quotes", "'''", "''''''"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapePowerShellString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSecurityInjectionPrevention tests that dangerous inputs are rejected
func TestSecurityInjectionPrevention(t *testing.T) {
	// Common injection patterns
	injectionPatterns := []string{
		`"; Remove-VM -Name "other"`,
		`' ; Remove-VM -Name 'other'`,
		`$(Get-VM | Remove-VM)`,
		"`; Remove-VM",
		"vm & Remove-VM",
		"vm | Remove-VM",
		"vm; Remove-VM",
		"vm` Remove-VM",
	}

	t.Run("VM name injection prevention", func(t *testing.T) {
		for _, pattern := range injectionPatterns {
			err := validateVMName(pattern)
			assert.Error(t, err, "Should reject injection pattern: %s", pattern)
		}
	})

	t.Run("Snapshot name injection prevention", func(t *testing.T) {
		// Snapshots allow spaces, so filter patterns accordingly
		dangerousPatterns := []string{
			`snapshot"; Remove-VM`,
			`snapshot$(Remove-VM)`,
			"snapshot`; cmd",
			"snapshot; Remove-VM",
		}
		for _, pattern := range dangerousPatterns {
			err := validateSnapshotName(pattern)
			assert.Error(t, err, "Should reject injection pattern: %s", pattern)
		}
	})

	t.Run("Path injection prevention", func(t *testing.T) {
		for _, pattern := range injectionPatterns {
			fullPath := "C:\\VMs\\" + pattern
			err := validatePath(fullPath)
			assert.Error(t, err, "Should reject injection pattern in path: %s", pattern)
		}
	})
}

// TestPathTraversalPrevention tests that path traversal is prevented
func TestPathTraversalPrevention(t *testing.T) {
	pathTraversalPatterns := []string{
		"../../../etc/passwd",
		"..\\..\\Windows\\System32",
		"image/../../../etc",
		"..\\..\\config",
	}

	for _, pattern := range pathTraversalPatterns {
		err := validateImageName(pattern)
		assert.Error(t, err, "Should reject path traversal: %s", pattern)
	}
}
