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
//
// ExecuteCommand builds and invokes a pipeline via PSRP's native
// AddCommand/AddArgument (go-psrp v0.2.2-boxy244, #244) instead of a text
// script, so a caller-controlled cmd/arg value never has to survive
// PowerShell's own parser -- it is delivered as a typed CLIXML argument
// object. Execute (the raw text-script entry point) is kept for ExecText's
// opaque-PowerShell-text use case, which has no argv to protect in the
// first place.
type psrpExecutor interface {
	Connect(ctx context.Context) error
	Execute(ctx context.Context, script string) (*psrpclient.Result, error)
	ExecuteCommand(ctx context.Context, cmdName string, isScript bool, args ...interface{}) (*psrpclient.Result, error)
	Close(ctx context.Context) error
}

type psrpStreamExecutor interface {
	psrpExecutor
	ExecuteStream(ctx context.Context, script string) (*psrpclient.StreamResult, error)
	ExecuteCommandStream(ctx context.Context, cmdName string, isScript bool, args ...interface{}) (*psrpclient.StreamResult, error)
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
//
// Exec connects, runs one command, and closes -- the full PSRP/WinRM session
// negotiation cost is paid on every call. A caller making several calls
// under the same credential back-to-back (e.g. hyperv's
// personalizeGuestLocked, #361) should prefer OpenSession to pay that cost
// once instead.
func (e *Exec) Exec(ctx context.Context, cmd string, args ...string) (*vmsdk.ExecResult, error) {
	executor, err := e.connectExecutor(ctx)
	if err != nil {
		return nil, err
	}
	defer executor.Close(ctx) //nolint:errcheck

	return runCommand(ctx, executor, e.VMID, cmd, args)
}

// ExecText executes opaque PowerShell text without converting it to argv.
// See Exec's doc comment for this method's per-call connection cost.
func (e *Exec) ExecText(ctx context.Context, text string) (*vmsdk.ExecResult, error) {
	executor, err := e.connectExecutor(ctx)
	if err != nil {
		return nil, err
	}
	defer executor.Close(ctx) //nolint:errcheck

	return runText(ctx, executor, e.VMID, text)
}

// OpenSession connects once and returns a vmsdk.GuestSession bound to that
// connection, implementing vmsdk.GuestSessionOpener. The caller may run any
// number of Exec/ExecText calls against the returned session before calling
// its Close -- each call reuses the one underlying PSRP connection instead
// of renegotiating a fresh session, closing exactly once when the caller is
// done. Streaming (ExecStream/ExecStreamText) is not available on a
// session; those keep their existing dedicated per-call connection.
func (e *Exec) OpenSession(ctx context.Context) (vmsdk.GuestSession, error) {
	executor, err := e.connectExecutor(ctx)
	if err != nil {
		return nil, err
	}
	return &Session{vmID: e.VMID, executor: executor}, nil
}

// connectExecutor creates and connects a psrpExecutor, wrapping both error
// paths identically for every caller (Exec, ExecText, OpenSession).
func (e *Exec) connectExecutor(ctx context.Context) (psrpExecutor, error) {
	executor, err := e.newExecutor(ctx)
	if err != nil {
		return nil, fmt.Errorf("psdirect: create client for VM %s: %w", e.VMID, err)
	}
	if err := executor.Connect(ctx); err != nil {
		return nil, fmt.Errorf("psdirect: connect to VM %s: %w", e.VMID, err)
	}
	return executor, nil
}

// Session is a vmsdk.GuestSession bound to one already-connected PSRP
// executor, returned by Exec.OpenSession. It lets a caller run multiple
// Exec/ExecText calls under one credential without paying a fresh
// Connect/Close cost per call (#361) -- see personalizeGuestLocked in
// pkg/providersdk/providers/hyperv for the motivating caller.
type Session struct {
	vmID     string
	executor psrpExecutor
}

// Exec runs cmd with args over the session's already-open connection.
func (s *Session) Exec(ctx context.Context, cmd string, args ...string) (*vmsdk.ExecResult, error) {
	return runCommand(ctx, s.executor, s.vmID, cmd, args)
}

// ExecText executes opaque PowerShell text over the session's already-open
// connection.
func (s *Session) ExecText(ctx context.Context, text string) (*vmsdk.ExecResult, error) {
	return runText(ctx, s.executor, s.vmID, text)
}

// Close ends the session's underlying connection. It is not valid to call
// Exec/ExecText on a Session after Close.
func (s *Session) Close(ctx context.Context) error {
	return s.executor.Close(ctx)
}

// runCommand invokes execScript via ExecuteCommand on an already-connected
// executor and extracts the result, shared by Exec.Exec and Session.Exec so
// the two entry points cannot drift apart.
func runCommand(ctx context.Context, executor psrpExecutor, vmID, cmd string, args []string) (*vmsdk.ExecResult, error) {
	result, err := executor.ExecuteCommand(ctx, execScript, true, commandArgs(cmd, args)...)
	if err != nil {
		return nil, fmt.Errorf("psdirect: exec on VM %s: %w", vmID, wrapKnownTransportError(err))
	}
	stdout, exitCode := extractOutput(result.Output)
	return &vmsdk.ExecResult{Stdout: stdout, ExitCode: exitCode}, nil
}

// runText executes opaque PowerShell text via Execute on an already-connected
// executor and extracts the result, shared by Exec.ExecText and
// Session.ExecText so the two entry points cannot drift apart.
func runText(ctx context.Context, executor psrpExecutor, vmID, text string) (*vmsdk.ExecResult, error) {
	script := text + "\n$LASTEXITCODE"
	result, err := executor.Execute(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("psdirect: exec text on VM %s: %w", vmID, wrapKnownTransportError(err))
	}
	stdout, exitCode := extractOutput(result.Output)
	return &vmsdk.ExecResult{Stdout: stdout, ExitCode: exitCode}, nil
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
	streamer, err := e.newStreamExecutor(ctx)
	if err != nil {
		return nil, err
	}
	defer streamer.Close(ctx) //nolint:errcheck

	stream, err := streamer.ExecuteCommandStream(ctx, execStreamScript, true, commandArgs(cmd, args)...)
	if err != nil {
		return nil, fmt.Errorf("psdirect: start stream on VM %s: %w", e.VMID, err)
	}
	return e.consumeStream(ctx, stream, sink)
}

// ExecStreamText streams opaque PowerShell text without converting it to argv.
func (e *Exec) ExecStreamText(ctx context.Context, text string, sink eventstream.Sink) (*vmsdk.ExecResult, error) {
	if sink == nil {
		return nil, fmt.Errorf("psdirect: stream sink is required")
	}
	streamer, err := e.newStreamExecutor(ctx)
	if err != nil {
		return nil, err
	}
	defer streamer.Close(ctx) //nolint:errcheck

	stream, err := streamer.ExecuteStream(ctx, buildStreamTextScript(text))
	if err != nil {
		return nil, fmt.Errorf("psdirect: start stream on VM %s: %w", e.VMID, err)
	}
	return e.consumeStream(ctx, stream, sink)
}

// newStreamExecutor creates and connects a psrpStreamExecutor, so both
// ExecStream and ExecStreamText share identical connect/type-assertion
// handling and differ only in which ExecuteStream* call they make.
func (e *Exec) newStreamExecutor(ctx context.Context) (psrpStreamExecutor, error) {
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
	return streamer, nil
}

// consumeStream fans in a StreamResult's channels, forwards them to sink via
// streamEmitter, and returns the final exit code once the pipeline
// completes. Shared by ExecStream and ExecStreamText -- everything after a
// stream has been started is identical regardless of how the pipeline was
// built.
func (e *Exec) consumeStream(ctx context.Context, stream *psrpclient.StreamResult, sink eventstream.Sink) (*vmsdk.ExecResult, error) {
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

// execScript and execStreamScript are fixed wrapper scripts: the text never
// contains caller-controlled data, so there is nothing in either script for
// PowerShell's own parser to mis-tokenize (the #238 bug class). cmd and args
// are instead bound via PSRP's native AddCommand(..., isScript: true) +
// AddArgument mechanism (go-psrp v0.2.2-boxy244, #244) -- each value is
// delivered as its own typed CLIXML argument object into the script's
// automatic $args array, exactly as `& { <script> } arg1 arg2` would bind
// them for a local script block. commandArgs builds that argument list.
//
// AddCommand/AddArgument only replaces argv *delivery* into PSRP -- it does
// not, by itself, get $LASTEXITCODE back out of a single-command pipeline
// (go-psrpcore's PowerShell object has no AddStatement to chain a second
// command), and it says nothing about how the *guest's* PowerShell 5.1
// reconstructs a native process's command line once $__boxyCmd/$__boxyArgs
// reach the `&` operator. So these scripts keep the exact `2>&1 | Out-String`
// / exit-marker shape the pre-#244 text-script builders used, and
// commandArgs keeps applying escapeNativeArg per argument -- see its doc
// comment for why that hazard is independent of how the argument value
// arrived at `&`.
const execScript = `$__boxyCmd = $args[0]
$__boxyArgs = @()
if ($args.Count -gt 1) { $__boxyArgs = $args[1..($args.Count - 1)] }
(& $__boxyCmd @__boxyArgs 2>&1) | Out-String
$LASTEXITCODE`

const execStreamScript = `$__boxyCmd = $args[0]
$__boxyArgs = @()
if ($args.Count -gt 1) { $__boxyArgs = $args[1..($args.Count - 1)] }
& $__boxyCmd @__boxyArgs 2>&1
Write-Output ('__BOXY_EXIT_CODE:' + [string]$LASTEXITCODE)`

// commandArgs builds the positional-argument list passed alongside execScript
// / execStreamScript: cmd first, then each of args, each still run through
// escapeNativeArg (see its doc comment) since that hazard sits downstream of
// PSRP argument delivery, at the guest's own native-process invocation.
func commandArgs(cmd string, args []string) []interface{} {
	out := make([]interface{}, 0, 1+len(args))
	out = append(out, escapeNativeArg(cmd))
	for _, a := range args {
		out = append(out, escapeNativeArg(a))
	}
	return out
}

// buildStreamTextScript appends the exit-marker line ExecStreamText's
// streaming path uses to recover $LASTEXITCODE. text is opaque caller-
// supplied PowerShell text with no argv of its own to protect -- this
// mirrors execScript's exit-code convention but is otherwise unrelated to
// the AddCommand/AddArgument argv path above.
func buildStreamTextScript(text string) string {
	return text + "\nWrite-Output ('__BOXY_EXIT_CODE:' + [string]$LASTEXITCODE)"
}

// extractOutput parses the PSRP output stream produced by execScript.
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
// command-line reconstruction for the `&` call operator.
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
// This hazard is a property of how PowerShell 5.1's `&` operator marshals
// an array of string values into a single native process command line --
// it applies whether those values came from parsed script text or, as of
// #244, from CLIXML argument objects delivered via PSRP's
// AddCommand/AddArgument and bound to $args. #244 removed a *different*,
// PowerShell-*parser*-level hazard (a caller-controlled value embedded in
// script text needing to survive PowerShell's own single-quoted-string
// tokenization, formerly handled by the now-removed psQuote) by no longer
// putting caller data in script text at all. It did not remove this one,
// which sits downstream at the guest's native-process invocation
// boundary -- so this function is still applied to every argument in
// commandArgs, unconditionally, matching the pre-#244 native-command
// scope (a cmdlet/function invocation via `&`/AddCommand needs no such
// escaping at all, but distinguishing that from a native executable would
// require resolving the command in the guest first).
//
// See #238 for the originally reported defect. The algorithm itself was
// verified live against Windows PowerShell 5.1.26100 by invoking `&`
// directly with hand-escaped literals; the no-whitespace
// quote-plus-trailing-backslash case (the one case the direct-`&` matrix
// could not settle on inspection alone, since it hinges on whether
// PowerShell wraps a whitespace-free value at all) was separately verified
// end-to-end through the pre-#244 psQuote's single-quote wrapper into a
// native argv dumper, confirmed to round-trip. This package still has no
// way to run a real PSRP session against a Windows guest from this host
// (see AGENTS.md), so the #244 $args/AddArgument delivery path beyond
// script generation remains unverified against a real guest, same as the
// pre-#244 script-text path was. One specific unverified case: an empty
// string argument produces no whitespace for PowerShell to quote around,
// so its native-command-line reconstruction may drop it from the child
// process's argv entirely rather than passing it through as an empty
// argument. This is not a #244 regression -- the pre-#244 psQuote("")
// path (`”`) had the same or worse fate -- but it means "empty-string
// arguments now work" is not a claim this change can make either.
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
