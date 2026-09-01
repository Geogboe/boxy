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
	executor, err := e.newExecutor(ctx)
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
		return nil, fmt.Errorf("psdirect: exec on VM %s: %w", e.VMID, wrapKnownTransportError(err))
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
	executor, err := e.newExecutor(ctx)
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
	emitter := newStreamEmitter(sink)
	for {
		select {
		case <-ctx.Done():
			stream.Cancel()
			return nil, ctx.Err()
		case item, ok := <-items:
			if !ok {
				if err := <-waitCh; err != nil {
					return nil, fmt.Errorf("psdirect: stream on VM %s: %w", e.VMID, wrapKnownTransportError(err))
				}
				return &vmsdk.ExecResult{ExitCode: emitter.exitCode}, nil
			}
			if item.msg == nil {
				continue
			}
			values, decodeErr := decodeMessage(item.msg)
			if decodeErr != nil {
				values = []interface{}{string(item.msg.Data)}
			}
			if err := emitter.emit(ctx, item.channel, values); err != nil {
				stream.Cancel()
				return nil, err
			}
		}
	}
}

// streamEmitter applies ExecStream's per-value formatting, exit-marker
// detection, and newline-separator insertion, then sends the result to a
// sink. It is factored out of ExecStream's per-item loop specifically so
// this logic -- the exact logic #247 found was missing from the shipped
// exec path, despite #239 having already fixed it in extractOutput's
// sibling loop -- is directly unit-testable against a fake eventstream.Sink,
// without needing a constructible *psrpclient.StreamResult (see ADR-0008's
// noted ExecStream coverage gap). ExecStream itself supplies the real sink
// and owns the fan-in/Wait/Cancel orchestration around this.
type streamEmitter struct {
	sink     eventstream.Sink
	trackers map[eventstream.Channel]*newlineTracker
	exitCode int
}

func newStreamEmitter(sink eventstream.Sink) *streamEmitter {
	return &streamEmitter{sink: sink, trackers: make(map[eventstream.Channel]*newlineTracker)}
}

// emit formats and sends each decoded value on channel, skipping the exit
// marker (stdout only -- recorded into e.exitCode instead of being sent)
// and any value formatStreamValue drops. A separate newlineTracker is kept
// per output channel, since stdout and stderr are consumed as independent
// concatenated streams by both public exec paths (internal/server/
// api_exec.go's bufferedExecSink and the CLI's live renderer), and each
// merges several underlying PSRP streams (Errors, Warnings, Verbose, Debug,
// Progress, Information all land on "stderr") that must still be separated
// from one another. Returns the first send error, if any.
func (e *streamEmitter) emit(ctx context.Context, channel eventstream.Channel, values []interface{}) error {
	for _, value := range values {
		if channel == eventstream.Channel("stdout") {
			if code, ok := parseExitMarker(value); ok {
				e.exitCode = code
				continue
			}
		}
		text, drop := formatStreamValue(value)
		if drop {
			continue
		}
		tracker, ok := e.trackers[channel]
		if !ok {
			tracker = &newlineTracker{}
			e.trackers[channel] = tracker
		}
		payload := []byte(tracker.next(text))
		if err := e.sink.Send(ctx, eventstream.Event{Kind: eventstream.Data, Channel: channel, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
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
func (e *Exec) newExecutor(ctx context.Context) (psrpExecutor, error) {
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
	cfg.Timeout = operationTimeout(ctx)

	return psrpclient.New("", cfg)
}

// defaultOperationTimeout is used when ctx carries no deadline. It matches
// this package's previous hardcoded cfg.Timeout value, so a caller that
// doesn't set a deadline sees unchanged behavior.
const defaultOperationTimeout = 30 * time.Second

// operationTimeout derives psrpclient.Config.Timeout (which bounds
// operations such as runspace-slot semaphore acquisition, not the transport's
// own idle-read cap -- see wrapKnownTransportError) from ctx's deadline, so a
// caller's real request timeout (internal/server/api_exec.go's
// context.WithTimeout, clamped to a 5m maximum) actually reaches the PSRP
// client instead of being silently overridden by a fixed value. Boxy's own
// entry points always set a deadline; the no-deadline fallback exists for
// direct callers of this package (tests, future integrations) that don't.
func operationTimeout(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultOperationTimeout
	}
	if remaining := time.Until(deadline); remaining > 0 {
		return remaining
	}
	return defaultOperationTimeout
}

// knownTransportErrSubstr identifies go-psrpcore's outofproc.Adapter.Read
// idle-read error -- originally a hardcoded literal (30s of silence on the
// wire, reset by any byte arriving) with no configuration knob, unrelated to
// cfg.Timeout/operationTimeout above and unaffected by --timeout. As of the
// go-psrp/go-psrpcore forks this module now depends on (go.mod's replace
// directives, see AGENTS.md's "PSRP Transport Dependency Fork" section),
// go-psrp's HvSocketBackend.Connect disables this cap entirely
// (Adapter.SetIdleReadTimeout(0)) in favor of the real ctx deadline, so this
// error should no longer actually occur on boxy's HvSocket path. This
// detection is kept as a defensive fallback -- a stale build against the
// unforked upstream, or a future adapter/backend path that doesn't disable
// the cap -- so the message stays actionable if it's ever hit again, rather
// than being removed and silently regressing to the opaque wrap below.
const knownTransportErrSubstr = "read timeout: no data received in 30s"

// wrapKnownTransportError adds an explanatory prefix when err is (or wraps)
// go-psrpcore's fixed idle-read timeout, so the caller sees that a timeout
// occurred and that (on an unforked build) it would be a fixed
// transport-level cap independent of the request's own --timeout, rather
// than an opaque "runspace pool broken" message. Other errors pass through
// unchanged.
func wrapKnownTransportError(err error) error {
	if err == nil || !strings.Contains(err.Error(), knownTransportErrSubstr) {
		return err
	}
	return fmt.Errorf("guest produced no output for a fixed ~30s and the underlying PSRP transport gave up (independent of --timeout, which only bounds the overall request): %w", err)
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
// The last numeric item is the exit code; everything else is stdout, joined
// with a newline between items that don't already end in one (Out-String
// output typically already carries its own \r\n, so an unconditional join
// would insert blank lines) via newlineTracker. Items formatStreamValue
// can't meaningfully render are dropped rather than turned into synthetic
// tokens -- see #239.
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
	tracker := &newlineTracker{}
	for _, item := range stdoutItems {
		text, drop := formatStreamValue(item)
		if drop {
			continue
		}
		sb.WriteString(tracker.next(text))
	}
	return sb.String(), exitCode
}

// newlineTracker inserts a leading separator before a stream item that
// doesn't begin a fresh line, so consecutive items lacking their own
// trailing newline don't get concatenated into one run-on line. Shared
// between extractOutput's single accumulated stream and streamEmitter's
// per-channel live loop so the two can't drift out of sync again the way
// they did for #247: #239 added this exact logic to extractOutput, but
// ExecStream's per-item loop -- the code path both public exec APIs
// actually call -- never got it.
//
// The zero value is ready to use: needsSeparator starts false, so a
// tracker's first item never gets a separator without an explicit
// constructor.
type newlineTracker struct {
	needsSeparator bool
}

// next returns text, prefixed with a newline if the previous text this
// tracker saw didn't already end in one. The separator decision for the
// *next* call is derived from this call's original text, not the prefixed
// result, so an empty item still correctly requires a separator before
// whatever follows it (an unprefixed "" would otherwise look newline-
// terminated and wrongly suppress the next separator).
func (t *newlineTracker) next(text string) string {
	out := text
	if t.needsSeparator {
		out = "\n" + text
	}
	t.needsSeparator = !strings.HasSuffix(text, "\n")
	return out
}

// formatStreamValue renders one deserialized PSRP stream item as text
// suitable for stdout/stderr. It drops nil values and the case where a
// *serialization.PSObject's own String() falls all the way through to its
// literal "PSObject" placeholder (no ToString, wrapped Value, exception
// message, or TypeNames) -- in that exact case there is nothing real to
// render, and emitting the literal token is worse than silence. Anything
// else formats via %v as before, which already calls PSObject.String() for
// its full ToString/Value/exception-record/TypeNames fallback chain -- this
// deliberately does not re-derive any subset of that chain itself, so it
// stays correct as PSObject's own fallback logic evolves. See #239.
func formatStreamValue(v interface{}) (text string, drop bool) {
	if v == nil {
		return "", true
	}
	text = fmt.Sprintf("%v", v)
	if _, ok := v.(*serialization.PSObject); ok && text == "PSObject" {
		return "", true
	}
	return text, false
}

// escapeNativeArg escapes s so it survives Windows PowerShell 5.1's native
// command-line reconstruction for the `&` call operator, on top of whatever
// psQuote does for the PowerShell parser itself.
//
// PowerShell performs no escaping of its own here: it joins already-parsed
// argument values with spaces and wraps a value in a bare `"..."` pair only
// if that value contains whitespace. Any embedded `"` or trailing `\` is
// then reinterpreted by the *target* executable's own argv parser
// (CommandLineToArgvW-style, used by effectively all native Windows
// executables, including powershell.exe itself): a run of N backslashes
// immediately before a `"` must become 2N+1 backslashes for the quote to
// survive as a literal character, and a run of N backslashes immediately
// before a closing `"` that PowerShell adds must be even, or that closing
// quote is swallowed as literal content instead of terminating the
// argument. This mirrors the algorithm behind Go's own syscall.EscapeArg
// (Windows argv escaping) minus the outer quote characters, which
// PowerShell -- not this function -- adds.
//
// This is a documented stopgap, not the long-term fix: it only patches the
// text-command-line-reconstruction hazard PowerShell's `&` operator creates
// in the first place. The real fix is to stop going through a text command
// line at all -- go-psrpcore's pipeline.Pipeline already supports invoking
// a command via AddCommand/AddArgument, PSRP's native equivalent of a
// parameterized query, which this bug class cannot occur against. Doing so
// requires a client-level API this package's go-psrp dependency doesn't
// currently expose; tracked in #244.
//
// See #238 for the reported defect. The algorithm itself was verified live
// against Windows PowerShell 5.1.26100 by invoking `&` directly with
// hand-escaped literals; the no-whitespace quote-plus-trailing-backslash
// case (the one case the direct-`&` matrix could not settle on inspection
// alone, since it hinges on whether PowerShell wraps a whitespace-free
// value at all) was separately verified end-to-end through psQuote's own
// single-quote wrapper into a native argv dumper, confirmed to round-trip.
// Coverage stops at the generated script string, though: this package has
// no way to run a real PSRP session against a Windows guest from this host
// (see AGENTS.md), so the psQuote -> PSRP wire serialization -> guest
// runspace path beyond script generation remains unverified against a real
// guest.
func escapeNativeArg(s string) string {
	hasSpace := strings.ContainsAny(s, " \t")
	var b strings.Builder
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			b.WriteRune(r)
		case '"':
			for ; slashes > 0; slashes-- {
				b.WriteByte('\\')
			}
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			slashes = 0
			b.WriteRune(r)
		}
	}
	if hasSpace {
		for ; slashes > 0; slashes-- {
			b.WriteByte('\\')
		}
	}
	return b.String()
}

// psQuote wraps s in a PowerShell single-quoted string literal, first
// escaping it for native command-line reconstruction (escapeNativeArg, for
// the & operator's argv rebuild -- see #238) and then doubling embedded
// single quotes (for the PowerShell parser's own single-quoted-string
// rule). These two escaping passes touch disjoint character classes --
// backslash and double quote vs. single quote -- so order between them does
// not matter; single-quote doubling is applied last only because it must
// wrap the whole already-escaped literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(escapeNativeArg(s), "'", "''") + "'"
}
