package psdirect

import (
	"context"
	"fmt"
	"strings"
	"time"

	psrpclient "github.com/smnsjas/go-psrp/client"

	"github.com/Geogboe/boxy/pkg/vmsdk"
)

// psrpExecutor is the minimal go-psrp surface used by this package.
// *psrpclient.Client satisfies this interface; tests inject a mock.
type psrpExecutor interface {
	Connect(ctx context.Context) error
	Execute(ctx context.Context, script string) (*psrpclient.Result, error)
	Close(ctx context.Context) error
}

// Exec implements vmsdk.GuestExec via PowerShell Direct (HvSocket/PSRP).
// It communicates with the VM guest using the PSRP wire protocol natively —
// no powershell.exe subprocess is required.
//
// The guest must have PowerShell remoting enabled (enabled by default on
// Windows Server; run Enable-PSRemoting on Windows 10/11).
type Exec struct {
	// VMID is the Hyper-V VM GUID (the resource ID from the hyperv driver).
	VMID string

	// Username and Password are the guest OS credentials for PSRP authentication.
	Username string
	Password string

	// Domain is the guest domain for authentication ("." for local accounts).
	// Defaults to "." if empty.
	Domain string

	// execFactory creates the PSRP executor; nil → real go-psrp HvSocket client.
	// Inject a mock in tests.
	execFactory func() (psrpExecutor, error)
}

// New returns a PowerShell Direct executor for a VM identified by its GUID.
func New(vmID, username, password string) *Exec {
	return &Exec{
		VMID:     vmID,
		Username: username,
		Password: password,
		Domain:   ".",
	}
}

// Exec runs cmd with args on the Windows guest via PowerShell Direct (HvSocket).
// Stdout is captured via Out-String; $LASTEXITCODE is returned as the exit code.
func (e *Exec) Exec(ctx context.Context, cmd string, args ...string) (*vmsdk.ExecResult, error) {
	executor, err := e.newExecutor()
	if err != nil {
		return nil, fmt.Errorf("psdirect: create client for VM %s: %w", e.VMID, err)
	}

	if err := executor.Connect(ctx); err != nil {
		return nil, fmt.Errorf("psdirect: connect to VM %s: %w", e.VMID, err)
	}
	defer executor.Close(ctx) //nolint:errcheck

	script := buildScript(cmd, args)

	result, err := executor.Execute(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("psdirect: exec on VM %s: %w", e.VMID, err)
	}

	stdout, exitCode := extractOutput(result.Output)
	return &vmsdk.ExecResult{
		Stdout:   stdout,
		ExitCode: exitCode,
	}, nil
}

// newExecutor returns a psrpExecutor, using the injected factory if set.
func (e *Exec) newExecutor() (psrpExecutor, error) {
	if e.execFactory != nil {
		return e.execFactory()
	}

	domain := e.Domain
	if domain == "" {
		domain = "."
	}

	cfg := psrpclient.DefaultConfig()
	cfg.Transport = psrpclient.TransportHvSocket
	cfg.VMID = e.VMID
	cfg.Username = e.Username
	cfg.Password = e.Password
	cfg.Domain = domain
	cfg.Timeout = 30 * time.Second

	return psrpclient.New("", cfg)
}

// buildScript constructs the PowerShell script that runs the command and
// emits stdout (via Out-String) followed by $LASTEXITCODE as a separate object.
//
// The output stream will be [string, int32]:
//   - string: combined stdout+stderr (2>&1)
//   - int32:  process exit code via $LASTEXITCODE
func buildScript(cmd string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, psQuote(cmd))
	for _, a := range args {
		parts = append(parts, psQuote(a))
	}
	return fmt.Sprintf("(& %s 2>&1) | Out-String\n$LASTEXITCODE",
		strings.Join(parts, " "))
}

// extractOutput parses the PSRP output stream produced by buildScript.
// The last numeric item is the exit code; everything else is stdout.
func extractOutput(output []interface{}) (stdout string, exitCode int) {
	if len(output) == 0 {
		return "", 0
	}

	// Detect whether the last item is the $LASTEXITCODE integer.
	last := output[len(output)-1]
	stdoutItems := output

	switch v := last.(type) {
	case int32:
		exitCode = int(v)
		stdoutItems = output[:len(output)-1]
	case int64:
		exitCode = int(v)
		stdoutItems = output[:len(output)-1]
	}

	var sb strings.Builder
	for _, item := range stdoutItems {
		switch v := item.(type) {
		case string:
			sb.WriteString(v)
		default:
			fmt.Fprintf(&sb, "%v", v)
		}
	}
	return sb.String(), exitCode
}

// psQuote wraps s in PowerShell single quotes, escaping contained single quotes.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
