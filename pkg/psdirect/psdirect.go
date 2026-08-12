package psdirect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	psrpclient "github.com/smnsjas/go-psrp/client"
	"github.com/smnsjas/go-psrpcore/messages"
	"github.com/smnsjas/go-psrpcore/serialization"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

// psrpExecutor is the minimal go-psrp surface used by this package.
// *psrpclient.Client satisfies this interface; tests inject a mock.
type psrpExecutor interface {
	Connect(ctx context.Context) error
	Execute(ctx context.Context, script string) (*psrpclient.Result, error)
	Close(ctx context.Context) error
}

type psrpStreamExecutor interface {
	psrpExecutor
	ExecuteStream(ctx context.Context, script string) (*psrpclient.StreamResult, error)
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

// ExecStream runs cmd through PowerShell Direct and forwards PSRP output as it
// arrives. PowerShell's merged native output is represented on stdout; PSRP
// error, warning, verbose, debug, progress, and information records are all
// merged onto stderr — none of those are exclusively "errors", so a caller
// deciding whether a command failed should rely on the exit code, not on
// whether anything arrived on the stderr channel.
func (e *Exec) ExecStream(ctx context.Context, cmd string, args []string, sink eventstream.Sink) (*vmsdk.ExecResult, error) {
	if sink == nil {
		return nil, fmt.Errorf("psdirect: stream sink is required")
	}
	executor, err := e.newExecutor()
	if err != nil {
		return nil, fmt.Errorf("psdirect: create client for VM %s: %w", e.VMID, err)
	}
	streamer, ok := executor.(psrpStreamExecutor)
	if !ok {
		return nil, fmt.Errorf("psdirect: streaming is not supported by the executor")
	}
	if err := streamer.Connect(ctx); err != nil {
		return nil, fmt.Errorf("psdirect: connect to VM %s: %w", e.VMID, err)
	}
	defer streamer.Close(ctx) //nolint:errcheck

	stream, err := streamer.ExecuteStream(ctx, buildStreamScript(cmd, args))
	if err != nil {
		return nil, fmt.Errorf("psdirect: start stream on VM %s: %w", e.VMID, err)
	}

	type streamItem struct {
		channel eventstream.Channel
		msg     *messages.Message
	}
	items := make(chan streamItem)
	var forwardWG sync.WaitGroup
	forward := func(channel eventstream.Channel, source <-chan *messages.Message) {
		forwardWG.Add(1)
		go func() {
			defer forwardWG.Done()
			for msg := range source {
				select {
				case items <- streamItem{channel: channel, msg: msg}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	forward(eventstream.Channel("stdout"), stream.Output)
	forward(eventstream.Channel("stderr"), stream.Errors)
	forward(eventstream.Channel("stderr"), stream.Warnings)
	forward(eventstream.Channel("stderr"), stream.Verbose)
	forward(eventstream.Channel("stderr"), stream.Debug)
	forward(eventstream.Channel("stderr"), stream.Progress)
	forward(eventstream.Channel("stderr"), stream.Information)

	waitCh := make(chan error, 1)
	go func() {
		err := stream.Wait()
		forwardWG.Wait()
		close(items)
		waitCh <- err
	}()
	exitCode := 0
	for {
		select {
		case <-ctx.Done():
			stream.Cancel()
			return nil, ctx.Err()
		case item, ok := <-items:
			if !ok {
				if err := <-waitCh; err != nil {
					return nil, fmt.Errorf("psdirect: stream on VM %s: %w", e.VMID, err)
				}
				return &vmsdk.ExecResult{ExitCode: exitCode}, nil
			}
			if item.msg == nil {
				continue
			}
			values, decodeErr := decodeMessage(item.msg)
			if decodeErr != nil {
				values = []interface{}{string(item.msg.Data)}
			}
			for _, value := range values {
				if item.channel == eventstream.Channel("stdout") {
					if code, ok := parseExitMarker(value); ok {
						exitCode = code
						continue
					}
				}
				payload := fmt.Append(nil, value)
				if len(payload) == 0 {
					continue
				}
				if err := sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: item.channel, Payload: payload}); err != nil {
					stream.Cancel()
					return nil, err
				}
			}
		}
	}
}

func decodeMessage(msg *messages.Message) ([]interface{}, error) {
	deserializer := serialization.NewDeserializer()
	defer deserializer.Close()
	return deserializer.Deserialize(msg.Data)
}

func parseExitMarker(value interface{}) (int, bool) {
	marker, ok := value.(string)
	if !ok || !strings.HasPrefix(marker, "__BOXY_EXIT_CODE:") {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimPrefix(marker, "__BOXY_EXIT_CODE:"))
	return code, err == nil
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

func buildStreamScript(cmd string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, psQuote(cmd))
	for _, a := range args {
		parts = append(parts, psQuote(a))
	}
	return fmt.Sprintf("& %s 2>&1\nWrite-Output ('__BOXY_EXIT_CODE:' + [string]$LASTEXITCODE)", strings.Join(parts, " "))
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
