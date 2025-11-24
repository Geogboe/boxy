// Package powershell provides a minimal wrapper around powershell.exe for
// executing PowerShell commands from Go. It offers structured output handling,
// context-aware cancellation, and safe command invocation with JSON marshalling
// support.
//
// Overview
//
// The executor runs each script via exec.CommandContext in non-interactive
// mode, capturing stdout, stderr, exit codes, and respecting context timeouts.
// Commands are passed as strings without shell interpolation to avoid injection
// issues. Exec returns raw string output, while ExecJSON unmarshals JSON output
// into a caller-supplied struct.
//
// Guarantees
//
//   - Context cancellation and deadlines are enforced.
//   - Stderr is captured and included in errors.
//   - JSON unmarshalling errors include the original output.
//   - No interactive prompts (PowerShell -NonInteractive).
//   - No use of shells beyond powershell.exe.
//
// Requirements
//
// The host must be Windows with powershell.exe available. Each invocation is
// synchronous and stateless; the package does not maintain persistent
// PowerShell sessions.
//
// Intended Use
//
// The package is used by Hyper-V–related components that rely on PowerShell
// for VM inspection and management, as well as by psdirect for PowerShell
// Direct invocation inside guest VMs.
//
// Limitations
//
//   - Windows-only (PowerShell 5.1+).
//   - No persistent runspaces or pipelines.
//   - No parameter marshalling beyond JSON-based result parsing.
//   - No streaming or asynchronous output collection.
//
// In short, powershell provides a small, safe execution layer around
// powershell.exe for components that need structured PowerShell interaction from Go.
package powershell
