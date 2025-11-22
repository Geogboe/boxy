package hyperv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// psExecutor handles PowerShell command execution
type psExecutor struct {
	logger *logrus.Logger
}

// newPSExecutor creates a new PowerShell executor
func newPSExecutor(logger *logrus.Logger) *psExecutor {
	return &psExecutor{logger: logger}
}

// exec executes a PowerShell command and returns the output
func (ps *psExecutor) exec(ctx context.Context, script string) (string, error) {
	ps.logger.WithField("script_length", len(script)).Debug("Executing PowerShell script")

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
		ps.logger.WithFields(logrus.Fields{
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
		ps.logger.WithField("stderr", stderrStr).Warn("PowerShell stderr output")
	}

	ps.logger.WithField("output_length", len(stdoutStr)).Debug("PowerShell script executed successfully")
	return stdoutStr, nil
}

// execJSON executes a PowerShell command and parses the JSON output
func (ps *psExecutor) execJSON(ctx context.Context, script string, result interface{}) error {
	output, err := ps.exec(ctx, script)
	if err != nil {
		return err
	}

	if output == "" {
		return fmt.Errorf("powershell returned empty output")
	}

	if err := json.Unmarshal([]byte(output), result); err != nil {
		ps.logger.WithFields(logrus.Fields{
			"output": output,
			"error":  err.Error(),
		}).Error("Failed to parse PowerShell JSON output")
		return fmt.Errorf("failed to parse powershell json: %w", err)
	}

	return nil
}

// vmInfo represents VM information returned by Get-VM
type vmInfo struct {
	Name              string    `json:"Name"`
	State             string    `json:"State"`
	CPUUsage          int       `json:"CPUUsage"`
	MemoryAssigned    int64     `json:"MemoryAssigned"`
	Uptime            string    `json:"Uptime"`
	Status            string    `json:"Status"`
	CreationTime      time.Time `json:"CreationTime"`
	ProcessorCount    int       `json:"ProcessorCount"`
	MemoryStartup     int64     `json:"MemoryStartup"`
	Generation        int       `json:"Generation"`
}

// networkAdapterInfo represents network adapter information
type networkAdapterInfo struct {
	VMName     string   `json:"VMName"`
	Name       string   `json:"Name"`
	IPAddresses []string `json:"IPAddresses"`
	MacAddress string   `json:"MacAddress"`
	Status     string   `json:"Status"`
}

// vhdInfo represents VHD information
type vhdInfo struct {
	Path          string `json:"Path"`
	VhdFormat     string `json:"VhdFormat"`
	VhdType       string `json:"VhdType"`
	FileSize      int64  `json:"FileSize"`
	Size          int64  `json:"Size"`
	ParentPath    string `json:"ParentPath"`
}

// checkpointInfo represents checkpoint (snapshot) information
type checkpointInfo struct {
	Name         string    `json:"Name"`
	VMName       string    `json:"VMName"`
	CreationTime time.Time `json:"CreationTime"`
	ParentCheckpointName string `json:"ParentCheckpointName"`
}
