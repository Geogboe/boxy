package psdirect

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/powershell"
)

// Client provides PowerShell Direct execution capabilities
// PowerShell Direct allows executing commands inside Hyper-V VMs
// without network connectivity, using VM integration services
type Client struct {
	ps     powershell.Commander
	logger *logrus.Logger
}

// NewClient creates a new PowerShell Direct client
func NewClient(ps powershell.Commander, logger *logrus.Logger) *Client {
	return &Client{
		ps:     ps,
		logger: logger,
	}
}

// ExecResult contains the result of command execution
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Error    error
}

// Exec executes a command inside a VM using PowerShell Direct
//
// Security: Commands are passed as DATA via -ArgumentList, not embedded code
// This prevents ScriptBlock injection attacks
func (c *Client) Exec(ctx context.Context, vmName string, creds Credentials, cmd []string) (*ExecResult, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("command cannot be empty")
	}

	c.logger.WithFields(logrus.Fields{
		"vm_name": vmName,
		"cmd":     cmd,
	}).Debug("Executing command via PowerShell Direct")

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
	`, escapePowerShellString(creds.Password), escapePowerShellString(creds.Username), cmdArrayStr, escapePowerShellString(vmName))

	output, err := c.ps.Exec(ctx, script)

	result := &ExecResult{
		ExitCode: 0,
		Stdout:   output,
		Stderr:   "",
	}

	if err != nil {
		result.ExitCode = 1
		result.Error = err
		result.Stderr = err.Error()
	}

	c.logger.WithFields(logrus.Fields{
		"vm_name":   vmName,
		"exit_code": result.ExitCode,
	}).Debug("Command executed via PowerShell Direct")

	return result, nil
}

// escapePowerShellString escapes single quotes in PowerShell strings
func escapePowerShellString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
