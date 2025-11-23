package powershell

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("with logger", func(t *testing.T) {
		logger := logrus.New()
		exec := New(logger)
		assert.NotNil(t, exec)
		assert.Equal(t, logger, exec.logger)
	})

	t.Run("with nil logger", func(t *testing.T) {
		exec := New(nil)
		assert.NotNil(t, exec)
		assert.NotNil(t, exec.logger)
	})
}

func TestExec(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell tests require Windows")
	}

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	exec := New(logger)

	t.Run("simple echo", func(t *testing.T) {
		ctx := context.Background()
		output, err := exec.Exec(ctx, "Write-Output 'Hello, World!'")
		require.NoError(t, err)
		assert.Equal(t, "Hello, World!", output)
	})

	t.Run("arithmetic", func(t *testing.T) {
		ctx := context.Background()
		output, err := exec.Exec(ctx, "Write-Output (2 + 2)")
		require.NoError(t, err)
		assert.Equal(t, "4", output)
	})

	t.Run("multiline output", func(t *testing.T) {
		ctx := context.Background()
		script := `
			Write-Output 'Line 1'
			Write-Output 'Line 2'
			Write-Output 'Line 3'
		`
		output, err := exec.Exec(ctx, script)
		require.NoError(t, err)
		assert.Contains(t, output, "Line 1")
		assert.Contains(t, output, "Line 2")
		assert.Contains(t, output, "Line 3")
	})

	t.Run("command failure", func(t *testing.T) {
		ctx := context.Background()
		_, err := exec.Exec(ctx, "Get-InvalidCommand")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "powershell failed")
	})

	t.Run("context timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Sleep longer than timeout
		_, err := exec.Exec(ctx, "Start-Sleep -Seconds 5")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "powershell failed")
	})

	t.Run("stderr warning", func(t *testing.T) {
		ctx := context.Background()
		// Write-Warning writes to stderr but doesn't fail
		output, err := exec.Exec(ctx, "Write-Warning 'This is a warning'; Write-Output 'Success'")
		require.NoError(t, err)
		assert.Equal(t, "Success", output)
	})
}

func TestExecJSON(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell tests require Windows")
	}

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	exec := New(logger)

	t.Run("simple object", func(t *testing.T) {
		ctx := context.Background()
		script := `
			$obj = @{
				Name = "TestVM"
				State = "Running"
				CPU = 2
			}
			$obj | ConvertTo-Json -Compress
		`

		var result map[string]interface{}
		err := exec.ExecJSON(ctx, script, &result)
		require.NoError(t, err)
		assert.Equal(t, "TestVM", result["Name"])
		assert.Equal(t, "Running", result["State"])
		assert.Equal(t, float64(2), result["CPU"]) // JSON numbers are float64
	})

	t.Run("typed struct", func(t *testing.T) {
		type VMInfo struct {
			Name  string `json:"Name"`
			State string `json:"State"`
			CPU   int    `json:"CPU"`
		}

		ctx := context.Background()
		script := `
			$obj = @{
				Name = "TestVM"
				State = "Running"
				CPU = 4
			}
			$obj | ConvertTo-Json -Compress
		`

		var result VMInfo
		err := exec.ExecJSON(ctx, script, &result)
		require.NoError(t, err)
		assert.Equal(t, "TestVM", result.Name)
		assert.Equal(t, "Running", result.State)
		assert.Equal(t, 4, result.CPU)
	})

	t.Run("array of objects", func(t *testing.T) {
		ctx := context.Background()
		script := `
			$arr = @(
				@{ Name = "VM1"; State = "Running" },
				@{ Name = "VM2"; State = "Stopped" }
			)
			$arr | ConvertTo-Json -Compress
		`

		var result []map[string]interface{}
		err := exec.ExecJSON(ctx, script, &result)
		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "VM1", result[0]["Name"])
		assert.Equal(t, "VM2", result[1]["Name"])
	})

	t.Run("invalid JSON", func(t *testing.T) {
		ctx := context.Background()
		script := `Write-Output 'This is not JSON'`

		var result map[string]interface{}
		err := exec.ExecJSON(ctx, script, &result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse powershell json")
	})

	t.Run("empty output", func(t *testing.T) {
		ctx := context.Background()
		script := `# Do nothing`

		var result map[string]interface{}
		err := exec.ExecJSON(ctx, script, &result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty output")
	})

	t.Run("command failure", func(t *testing.T) {
		ctx := context.Background()
		script := `Get-InvalidCommand`

		var result map[string]interface{}
		err := exec.ExecJSON(ctx, script, &result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "powershell failed")
	})
}
