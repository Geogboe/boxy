// Package psdirect provides a PowerShell Direct client for executing commands
// inside Hyper-V virtual machines without requiring network connectivity.
// It is used by the Hyper-V provider to run commands during provisioning and
// configuration, including before a VM has network access.
//
// Overview
//
// PowerShell Direct allows commands to be invoked inside a running VM via
// Hyper-V integration services. This package wraps that mechanism with a
// Go-friendly API that:
//   - Executes commands with supplied VM credentials
//   - Treats commands as data rather than embedded code
//   - Avoids PowerShell injection vulnerabilities
//   - Captures stdout, stderr, and exit codes
//
// Requirements
//
// The host must be Windows with Hyper-V enabled, the VM must be running and have
// integration services available, and valid guest credentials must be supplied.
// Network connectivity is not required.
//
// API Notes
//
// A Client is constructed with a PowerShell executor. Exec invokes a command
// inside a VM given its name, credentials, and an argument vector. Commands are
// passed as data through PowerShell’s -ArgumentList to avoid ScriptBlock
// injection risks. Result values include exit code, stdout, and stderr.
//
// Security
//
// The package uses an invocation pattern that treats the command to be executed
// as a parameter rather than interpolated code. This prevents injection attacks
// where untrusted command fragments could escape a ScriptBlock context.
//
// Dependencies
//
// The only external dependency is pkg/powershell, which provides the host-side
// PowerShell execution layer. The package contains no Hyper-V management logic;
// it focuses solely on PowerShell Direct invocation.
//
// In short, psdirect provides a safe, minimal wrapper around PowerShell Direct
// for issuing commands inside Hyper-V VMs during provisioning and management.
package psdirect
