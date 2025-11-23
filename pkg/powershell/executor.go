package powershell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

// Executor executes PowerShell commands from Go
type Executor struct {
	logger *logrus.Logger
}

// New creates a new PowerShell executor
func New(logger *logrus.Logger) *Executor {
	if logger == nil {
		logger = logrus.New()
	}
	return &Executor{logger: logger}
}

// Exec executes a PowerShell script and returns stdout
// Returns an error if the command fails or context is cancelled
func (e *Executor) Exec(ctx context.Context, script string) (string, error) {
	e.logger.WithField("script_length", len(script)).Debug("Executing PowerShell script")

	// Create command with context for timeout support
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute
	err := cmd.Run()

	stdoutStr := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())

	if err != nil {
		e.logger.WithFields(logrus.Fields{
			"error":  err.Error(),
			"stdout": stdoutStr,
			"stderr": stderrStr,
		}).Error("PowerShell command failed")

		// Include stderr in error message if available
		if stderrStr != "" {
			return stdoutStr, fmt.Errorf("powershell failed: %w: %s", err, stderrStr)
		}
		return stdoutStr, fmt.Errorf("powershell failed: %w", err)
	}

	// Log stderr even on success (warnings)
	if stderrStr != "" {
		e.logger.WithField("stderr", stderrStr).Warn("PowerShell stderr output")
	}

	e.logger.WithField("output_length", len(stdoutStr)).Debug("PowerShell script executed successfully")
	return stdoutStr, nil
}

// ExecJSON executes a PowerShell script and unmarshals JSON output into result
// The script should output JSON (e.g., using ConvertTo-Json in PowerShell)
func (e *Executor) ExecJSON(ctx context.Context, script string, result interface{}) error {
	output, err := e.Exec(ctx, script)
	if err != nil {
		return err
	}

	if output == "" {
		return fmt.Errorf("powershell returned empty output")
	}

	if err := json.Unmarshal([]byte(output), result); err != nil {
		e.logger.WithFields(logrus.Fields{
			"output": output,
			"error":  err.Error(),
		}).Error("Failed to parse PowerShell JSON output")
		return fmt.Errorf("failed to parse powershell json: %w", err)
	}

	return nil
}
