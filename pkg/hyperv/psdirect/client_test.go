package psdirect

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Geogboe/boxy/pkg/powershell"
)

// Unit tests with mocked PowerShell

func TestNewClient(t *testing.T) {
	t.Run("creates valid client", func(t *testing.T) {
		ps := powershell.NewMock()
		logger := logrus.New()

		client := NewClient(ps, logger)

		assert.NotNil(t, client)
		assert.NotNil(t, client.ps)
		assert.NotNil(t, client.logger)
	})
}

func TestExec(t *testing.T) {
	t.Run("executes command successfully", func(t *testing.T) {
		ps := powershell.NewMock()
		ps.WithExecResponse("command output", nil)

		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Administrator", "Password123!")

		result, err := client.Exec(
			context.Background(),
			"TestVM",
			creds,
			[]string{"powershell", "-Command", "Get-Process"},
		)

		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
		assert.Equal(t, "command output", result.Stdout)
		assert.Empty(t, result.Stderr)
		assert.Nil(t, result.Error)

		// Verify PowerShell was called
		assert.Len(t, ps.ExecCalls, 1)
		assert.Contains(t, ps.ExecCalls[0], "Invoke-Command")
		assert.Contains(t, ps.ExecCalls[0], "-VMName 'TestVM'")
		assert.Contains(t, ps.ExecCalls[0], "Administrator")
	})

	t.Run("handles command execution error", func(t *testing.T) {
		ps := powershell.NewMock()
		ps.WithExecResponse("", assert.AnError)

		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Administrator", "Password123!")

		result, err := client.Exec(
			context.Background(),
			"TestVM",
			creds,
			[]string{"invalid-command"},
		)

		require.NoError(t, err) // Exec doesn't return error, it's in result
		assert.Equal(t, 1, result.ExitCode)
		assert.NotNil(t, result.Error)
		assert.NotEmpty(t, result.Stderr)
	})

	t.Run("rejects empty command", func(t *testing.T) {
		ps := powershell.NewMock()
		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Administrator", "Password123!")

		result, err := client.Exec(
			context.Background(),
			"TestVM",
			creds,
			[]string{}, // Empty command
		)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "command cannot be empty")
	})

	t.Run("escapes single quotes in credentials", func(t *testing.T) {
		ps := powershell.NewMock()
		ps.WithExecResponse("output", nil)

		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Admin's'User", "Pass'word")

		result, err := client.Exec(
			context.Background(),
			"TestVM",
			creds,
			[]string{"echo", "test"},
		)

		require.NoError(t, err)
		assert.NotNil(t, result)

		// Verify escaping happened
		script := ps.ExecCalls[0]
		assert.Contains(t, script, "Admin''s''User")
		assert.Contains(t, script, "Pass''word")
	})

	t.Run("escapes single quotes in command arguments", func(t *testing.T) {
		ps := powershell.NewMock()
		ps.WithExecResponse("output", nil)

		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Administrator", "Password123!")

		result, err := client.Exec(
			context.Background(),
			"TestVM",
			creds,
			[]string{"echo", "it's", "a", "test's"},
		)

		require.NoError(t, err)
		assert.NotNil(t, result)

		// Verify escaping in command array
		script := ps.ExecCalls[0]
		assert.Contains(t, script, "'it''s'")
		assert.Contains(t, script, "'test''s'")
	})

	t.Run("passes context to powershell executor", func(t *testing.T) {
		ps := powershell.NewMock()
		ps.WithExecResponse("output", nil)

		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Administrator", "Password123!")

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Should still work because mock doesn't check context
		// Real implementation would respect context cancellation
		result, err := client.Exec(
			ctx,
			"TestVM",
			creds,
			[]string{"echo", "test"},
		)

		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("handles single argument command", func(t *testing.T) {
		ps := powershell.NewMock()
		ps.WithExecResponse("output", nil)

		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Administrator", "Password123!")

		result, err := client.Exec(
			context.Background(),
			"TestVM",
			creds,
			[]string{"hostname"},
		)

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 0, result.ExitCode)
	})

	t.Run("handles multi-argument command", func(t *testing.T) {
		ps := powershell.NewMock()
		ps.WithExecResponse("output", nil)

		client := NewClient(ps, logrus.New())
		creds := NewCredentials("Administrator", "Password123!")

		result, err := client.Exec(
			context.Background(),
			"TestVM",
			creds,
			[]string{"powershell", "-NoProfile", "-Command", "Get-Date"},
		)

		require.NoError(t, err)
		assert.NotNil(t, result)

		// Verify all arguments are in the script
		script := ps.ExecCalls[0]
		assert.Contains(t, script, "'powershell'")
		assert.Contains(t, script, "'-NoProfile'")
		assert.Contains(t, script, "'-Command'")
		assert.Contains(t, script, "'Get-Date'")
	})
}

func TestEscapePowerShellString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no quotes",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "single quote",
			input:    "it's",
			expected: "it''s",
		},
		{
			name:     "multiple quotes",
			input:    "it's a test's",
			expected: "it''s a test''s",
		},
		{
			name:     "only quotes",
			input:    "'''",
			expected: "''''''",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapePowerShellString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCredentials(t *testing.T) {
	t.Run("NewCredentials creates valid credentials", func(t *testing.T) {
		creds := NewCredentials("testuser", "testpass")

		assert.Equal(t, "testuser", creds.Username)
		assert.Equal(t, "testpass", creds.Password)
	})
}
