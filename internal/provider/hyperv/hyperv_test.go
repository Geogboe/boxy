package hyperv

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/crypto"
	provider_pkg "github.com/Geogboe/boxy/pkg/provider"
)

func TestProvider_Name(t *testing.T) {
	p := createTestProvider(t)
	assert.Equal(t, "hyperv", p.Name())
}

func TestProvider_Type(t *testing.T) {
	p := createTestProvider(t)
	assert.Equal(t, resource.ResourceTypeVM, p.Type())
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
