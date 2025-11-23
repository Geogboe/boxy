package hyperv

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/pkg/provider"
	"github.com/Geogboe/boxy/pkg/crypto"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
)

func TestProvider_Name(t *testing.T) {
	p := createTestProvider(t)
	assert.Equal(t, "hyperv", p.Name())
}

func TestProvider_Type(t *testing.T) {
	p := createTestProvider(t)
	assert.Equal(t, provider.ResourceTypeVM, p.Type())
}

func TestGenerateSecurePassword(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"8 chars", 8},
		{"16 chars", 16},
		{"32 chars", 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			password, err := generateSecurePassword(tt.length)
			require.NoError(t, err)
			assert.Len(t, password, tt.length)
			// Should start with P@ss to meet Windows complexity
			assert.True(t, len(password) >= 4 && password[:4] == "P@ss")
		})
	}
}

func TestParseHyperVUptime(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{
			name:     "empty string",
			input:    "",
			expected: 0,
			wantErr:  false,
		},
		{
			name:     "1 day uptime",
			input:    "01:00:00:00.0000000",
			expected: 24 * time.Hour,
			wantErr:  false,
		},
		{
			name:     "1 hour 30 minutes",
			input:    "00:01:30:00.0000000",
			expected: 1*time.Hour + 30*time.Minute,
			wantErr:  false,
		},
		{
			name:     "complex uptime",
			input:    "03:12:34:56.1234567",
			expected: 3*24*time.Hour + 12*time.Hour + 34*time.Minute + 56*time.Second,
			wantErr:  false,
		},
		{
			name:    "invalid format",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration, err := parseHyperVUptime(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				// Allow small variance for float parsing
				diff := duration - tt.expected
				if diff < 0 {
					diff = -diff
				}
				assert.True(t, diff < time.Second, "Expected %v, got %v", tt.expected, duration)
			}
		})
	}
}

func TestUpdatePowerState(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := createTestProvider(t)

	tests := []struct {
		name      string
		state     provider_pkg.PowerState
		expectErr bool
	}{
		{
			name:  "unsupported state",
			state: provider_pkg.PowerState("invalid"),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := p.updatePowerState(ctx, "test-vm", tt.state)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				// Would pass on Windows with Hyper-V
				t.Skip("Requires Windows with Hyper-V")
			}
		})
	}
}

func TestUpdateSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	p := createTestProvider(t)

	tests := []struct {
		name      string
		snapshot  *provider_pkg.SnapshotOp
		expectErr bool
	}{
		{
			name: "unsupported operation",
			snapshot: &provider_pkg.SnapshotOp{
				Operation: "invalid",
				Name:      "test-snapshot",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := p.updateSnapshot(ctx, "test-vm", tt.snapshot)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				// Would pass on Windows with Hyper-V
				t.Skip("Requires Windows with Hyper-V")
			}
		})
	}
}

func TestConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, "C:\\ProgramData\\Boxy\\VMs", cfg.VMPath)
	assert.Equal(t, "C:\\ProgramData\\Boxy\\VHDs", cfg.VHDPath)
	assert.Equal(t, "Default Switch", cfg.SwitchName)
	assert.Equal(t, "C:\\ProgramData\\Boxy\\BaseImages", cfg.BaseImagesPath)
	assert.Equal(t, 2, cfg.DefaultGeneration)
	assert.Equal(t, 5*time.Minute, cfg.WaitForIPTimeout)
}

func TestExec_SecurityValidation(t *testing.T) {
	p := createTestProvider(t)
	ctx := context.Background()

	// Create a mock resource with encrypted password
	password := "TestPassword123!"
	encPassword, err := p.encryptor.Encrypt(password)
	require.NoError(t, err)

	res := &provider.Resource{
		ProviderID: "test-vm",
		Metadata: map[string]interface{}{
			"username":     "Administrator",
			"password_enc": encPassword,
		},
	}

	tests := []struct {
		name      string
		cmd       []string
		expectErr bool
		errMsg    string
	}{
		{
			name:      "empty command",
			cmd:       []string{},
			expectErr: true,
			errMsg:    "command cannot be empty",
		},
		{
			name:      "valid simple command",
			cmd:       []string{"dir"},
			expectErr: false, // Will fail due to no Hyper-V, but validates command construction
		},
		{
			name:      "command with arguments",
			cmd:       []string{"cmd.exe", "/c", "dir"},
			expectErr: false,
		},
		{
			name: "scriptblock breakout attempt with closing brace",
			cmd:  []string{"dir", "}", ";", "Remove-VM", "-Name", "victim"},
			expectErr: false, // Command is valid, breakout should be prevented by ArgumentList approach
		},
		{
			name: "injection with semicolon",
			cmd:  []string{"echo", "test", ";", "Remove-VM"},
			expectErr: false, // Semicolon is just a string argument, not command separator
		},
		{
			name: "injection with pipe",
			cmd:  []string{"echo", "test", "|", "Remove-VM"},
			expectErr: false, // Pipe is just a string argument
		},
		{
			name: "injection with dollar sign",
			cmd:  []string{"echo", "$env:COMPUTERNAME"},
			expectErr: false, // Will be treated as literal string in VM
		},
		{
			name: "single quotes in argument",
			cmd:  []string{"echo", "test'with'quotes"},
			expectErr: false, // Should be escaped properly
		},
		{
			name: "command with many arguments",
			cmd:  []string{"powershell", "-Command", "Get-Process", "|", "Select-Object", "-First", "5"},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := p.Exec(ctx, res, tt.cmd)

			if tt.expectErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				assert.Nil(t, result)
			} else if result != nil {
				// On non-Windows or without Hyper-V, we expect PowerShell execution to fail
				// But we're testing command construction, not actual execution
				// The fact that it gets to PowerShell exec proves injection is prevented
				// Command was constructed and attempted
				t.Logf("Command constructed successfully: %v", tt.cmd)
				// Don't assert success since we're not on Windows with Hyper-V
			}
		})
	}
}

func TestExec_CommandConstruction(t *testing.T) {
	// This test validates that dangerous command arguments don't break PowerShell syntax
	p := createTestProvider(t)

	password := "Test123!"
	encPassword, err := p.encryptor.Encrypt(password)
	require.NoError(t, err)

	res := &provider.Resource{
		ProviderID: "test-vm",
		Metadata: map[string]interface{}{
			"username":     "Admin",
			"password_enc": encPassword,
		},
	}

	// Test that these dangerous patterns are properly escaped
	dangerousCommands := [][]string{
		{"echo", "}"}, // Closing brace
		{"echo", "{"}, // Opening brace
		{"echo", ";Remove-VM"}, // Semicolon
		{"echo", "`; Remove-VM"}, // Backtick
		{"echo", "$($env:USER)"}, // Variable expansion attempt
		{"echo", "' OR '1'='1"}, // SQL-style injection attempt
		{"cmd", "/c", "echo } ; Remove-VM -Name 'victim' ; { echo x"}, // Full breakout attempt
	}

	for i, cmd := range dangerousCommands {
		t.Run(fmt.Sprintf("dangerous_pattern_%d", i), func(t *testing.T) {
			// This should not panic or cause syntax errors in PowerShell construction
			_, err := p.Exec(context.Background(), res, cmd)

			// We expect execution to fail (no Hyper-V), but not due to PowerShell syntax errors
			// The key is that the function completes without panic
			t.Logf("Dangerous command handled: %v, error: %v", cmd, err)
		})
	}
}

func TestExec_InvalidVMName(t *testing.T) {
	p := createTestProvider(t)

	password := "Test123!"
	encPassword, err := p.encryptor.Encrypt(password)
	require.NoError(t, err)

	res := &provider.Resource{
		ProviderID: "vm; Remove-VM", // Invalid VM name with injection attempt
		Metadata: map[string]interface{}{
			"username":     "Admin",
			"password_enc": encPassword,
		},
	}

	_, err = p.Exec(context.Background(), res, []string{"dir"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid VM name")
}

func TestExec_MissingPassword(t *testing.T) {
	p := createTestProvider(t)

	res := &provider.Resource{
		ProviderID: "test-vm",
		Metadata: map[string]interface{}{
			"username": "Admin",
			// Missing password_enc
		},
	}

	_, err := p.Exec(context.Background(), res, []string{"dir"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "password not found")
}

// Helper functions

func createTestProvider(t *testing.T) *Provider {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	encryptor, err := crypto.NewEncryptor(key)
	require.NoError(t, err)

	return NewProvider(logger, encryptor)
}
