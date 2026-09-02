package psdirect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	psrpclient "github.com/smnsjas/go-psrp/client"
	"github.com/smnsjas/go-psrpcore/serialization"

	"github.com/Geogboe/boxy/pkg/eventstream"
)

// mockExecutor is a test double for psrpExecutor.
type mockExecutor struct {
	connectErr error
	execFunc   func(ctx context.Context, script string) (*psrpclient.Result, error)
}

func (m *mockExecutor) Connect(_ context.Context) error { return m.connectErr }
func (m *mockExecutor) Close(_ context.Context) error   { return nil }
func (m *mockExecutor) Execute(ctx context.Context, script string) (*psrpclient.Result, error) {
	return m.execFunc(ctx, script)
}

func makeExec(mock *mockExecutor) *Exec {
	e := New("test-guid", "admin", "${BOXY_TEST_PASSWORD}")
	e.execFactory = func() (psrpExecutor, error) { return mock, nil }
	return e
}

func TestExec_Exec_HappyPath(t *testing.T) {
	mock := &mockExecutor{
		execFunc: func(_ context.Context, _ string) (*psrpclient.Result, error) {
			return &psrpclient.Result{
				Output: []interface{}{"hello world\r\n", int32(0)},
			}, nil
		},
	}

	result, err := makeExec(mock).Exec(context.Background(), "echo", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello world") {
		t.Errorf("stdout %q does not contain expected output", result.Stdout)
	}
}

func TestExec_Exec_NonZeroExitCode(t *testing.T) {
	mock := &mockExecutor{
		execFunc: func(_ context.Context, _ string) (*psrpclient.Result, error) {
			return &psrpclient.Result{
				Output: []interface{}{"error output\r\n", int32(127)},
			}, nil
		},
	}

	result, err := makeExec(mock).Exec(context.Background(), "nonexistent-cmd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 127 {
		t.Errorf("exit code = %d, want 127", result.ExitCode)
	}
}

func TestExec_Exec_ConnectError(t *testing.T) {
	mock := &mockExecutor{
		connectErr: fmt.Errorf("vm not running"),
		execFunc: func(_ context.Context, _ string) (*psrpclient.Result, error) {
			return nil, nil
		},
	}

	_, err := makeExec(mock).Exec(context.Background(), "echo", "hi")
	if err == nil {
		t.Fatal("expected error when Connect fails")
	}
	if !strings.Contains(err.Error(), "connect to VM") {
		t.Errorf("error %q should mention connect to VM", err.Error())
	}
}

func TestExec_Exec_ExecuteError(t *testing.T) {
	mock := &mockExecutor{
		execFunc: func(_ context.Context, _ string) (*psrpclient.Result, error) {
			return nil, fmt.Errorf("pipeline failed")
		},
	}

	_, err := makeExec(mock).Exec(context.Background(), "echo")
	if err == nil {
		t.Fatal("expected error when Execute fails")
	}
	if !strings.Contains(err.Error(), "exec on VM") {
		t.Errorf("error %q should mention exec on VM", err.Error())
	}
}

func TestExec_Exec_QuotesArgs(t *testing.T) {
	var capturedScript string
	mock := &mockExecutor{
		execFunc: func(_ context.Context, script string) (*psrpclient.Result, error) {
			capturedScript = script
			return &psrpclient.Result{Output: []interface{}{int32(0)}}, nil
		},
	}

	makeExec(mock).Exec(context.Background(), "cmd", "arg with spaces", "it's quoted") //nolint:errcheck

	if !strings.Contains(capturedScript, "'arg with spaces'") {
		t.Errorf("expected quoted arg in script: %s", capturedScript)
	}
	if !strings.Contains(capturedScript, "'it''s quoted'") {
		t.Errorf("expected escaped single quote in script: %s", capturedScript)
	}
}

func TestExec_Exec_EmptyOutput(t *testing.T) {
	mock := &mockExecutor{
		execFunc: func(_ context.Context, _ string) (*psrpclient.Result, error) {
			return &psrpclient.Result{Output: []interface{}{}}, nil
		},
	}

	result, err := makeExec(mock).Exec(context.Background(), "echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestExec_Exec_FactoryError(t *testing.T) {
	e := New("test-guid", "admin", "${BOXY_TEST_PASSWORD}")
	e.execFactory = func() (psrpExecutor, error) {
		return nil, fmt.Errorf("client creation failed")
	}

	_, err := e.Exec(context.Background(), "echo")
	if err == nil {
		t.Fatal("expected error when execFactory fails")
	}
}

func TestExec_New(t *testing.T) {
	e := New("guid-123", "user", "${BOXY_TEST_PASSWORD}")
	if e.VMID != "guid-123" {
		t.Errorf("VMID = %q, want %q", e.VMID, "guid-123")
	}
	if e.Domain != "." {
		t.Errorf("Domain = %q, want %q", e.Domain, ".")
	}
	if e.execFactory != nil {
		t.Error("execFactory should be nil for real executor")
	}
}

// --- extractOutput unit tests ---

func TestExtractOutput_StringAndExitCode(t *testing.T) {
	stdout, code := extractOutput([]interface{}{"hello\r\n", int32(42)})
	if code != 42 {
		t.Errorf("exit code = %d, want 42", code)
	}
	if stdout != "hello\r\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\r\n")
	}
}

func TestExtractOutput_OnlyExitCode(t *testing.T) {
	stdout, code := extractOutput([]interface{}{int32(1)})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestExtractOutput_Empty(t *testing.T) {
	stdout, code := extractOutput(nil)
	if code != 0 || stdout != "" {
		t.Errorf("expected empty result, got stdout=%q code=%d", stdout, code)
	}
}

func TestExtractOutput_Int64ExitCode(t *testing.T) {
	_, code := extractOutput([]interface{}{"out", int64(5)})
	if code != 5 {
		t.Errorf("exit code = %d, want 5", code)
	}
}

func TestBuildStreamScriptUsesExitMarkerWithoutBuffering(t *testing.T) {
	script := buildStreamScript("myapp", []string{"arg1"})
	if strings.Contains(script, "Out-String") {
		t.Fatalf("stream script buffers output with Out-String: %s", script)
	}
	if !strings.Contains(script, "__BOXY_EXIT_CODE:") {
		t.Fatalf("stream script lacks exit marker: %s", script)
	}
	if code, ok := parseExitMarker("__BOXY_EXIT_CODE:17"); !ok || code != 17 {
		t.Fatalf("parseExitMarker = %d, %v; want 17, true", code, ok)
	}
}

// --- buildScript tests ---

func TestBuildScript_QuotesAndJoins(t *testing.T) {
	script := buildScript("myapp", []string{"arg1", "it's here"})
	if !strings.Contains(script, "'myapp'") {
		t.Errorf("expected quoted cmd in script: %s", script)
	}
	if !strings.Contains(script, "'it''s here'") {
		t.Errorf("expected escaped quote in script: %s", script)
	}
	if !strings.Contains(script, "$LASTEXITCODE") {
		t.Errorf("expected $LASTEXITCODE in script: %s", script)
	}
	if !strings.Contains(script, "Out-String") {
		t.Errorf("expected Out-String in script: %s", script)
	}
}

func TestBuildStreamTextScriptPreservesMultilineCRLFInput(t *testing.T) {
	const input = "Write-Output 'first'\r\nWrite-Output 'second'\r\n"
	script := buildStreamTextScript(input)
	if !strings.HasPrefix(script, input) {
		t.Fatalf("stream script changed opaque input: got prefix %q, want %q", script, input)
	}
	if !strings.Contains(script, "__BOXY_EXIT_CODE:") {
		t.Fatalf("stream script missing exit marker: %q", script)
	}
}

func TestBuildScript_QuotesEmbeddedDoubleQuotes(t *testing.T) {
	script := buildScript("powershell", []string{"-Command", `Write-Output "MARK[a b]"`})
	if !strings.Contains(script, `Write-Output \"MARK[a b]\"`) {
		t.Errorf("expected native-argv-escaped embedded quotes in script: %s", script)
	}
}

func TestBuildStreamScript_QuotesEmbeddedDoubleQuotes(t *testing.T) {
	script := buildStreamScript("powershell", []string{"-Command", `Write-Output "MARK[a b]"`})
	if !strings.Contains(script, `Write-Output \"MARK[a b]\"`) {
		t.Errorf("expected native-argv-escaped embedded quotes in script: %s", script)
	}
}

// --- escapeNativeArg / psQuote tests (#238) ---
//
// These pin the native-command-line-reconstruction escaping that
// psQuote applies on top of its own single-quote doubling. See #238 and
// psQuote's doc comment for the mechanism. This is a documented stopgap
// pending #244 (PSRP native AddCommand/AddArgument).

func TestEscapeNativeArg_EmbeddedQuote(t *testing.T) {
	got := escapeNativeArg(`Write-Output "MARK[a b]"`)
	want := `Write-Output \"MARK[a b]\"`
	if got != want {
		t.Errorf("escapeNativeArg = %q, want %q", got, want)
	}
}

func TestEscapeNativeArg_QuotePrecededByBackslash(t *testing.T) {
	// A literal backslash immediately before a literal quote must have its
	// own backslash-run doubled before the escaping backslash is added, or
	// the byte-count parity comes out wrong on the guest's argv parser. This
	// is the case that proves a naive "just backslash-escape quotes" fix
	// (the issue's own minimal suggestion) is wrong, not merely incomplete.
	got := escapeNativeArg(`foo\"bar`)
	want := `foo\\\"bar`
	if got != want {
		t.Errorf("escapeNativeArg = %q, want %q", got, want)
	}
}

func TestEscapeNativeArg_TrailingBackslashWithSpace(t *testing.T) {
	got := escapeNativeArg(`C:\some path\`)
	want := `C:\some path\\`
	if got != want {
		t.Errorf("escapeNativeArg = %q, want %q", got, want)
	}
}

func TestEscapeNativeArg_TrailingBackslashNoSpace(t *testing.T) {
	// No whitespace means PowerShell never wraps this value in a quote, so
	// there is nothing for a trailing backslash run to collide with -- it
	// must stay untouched. Doubling unconditionally would be a regression.
	got := escapeNativeArg(`C:\`)
	want := `C:\`
	if got != want {
		t.Errorf("escapeNativeArg = %q, want %q", got, want)
	}
}

func TestEscapeNativeArg_QuoteAndTrailingBackslashNoSpace(t *testing.T) {
	// The decisive case for whether hasSpace should gate on the pre-escape
	// or post-escape string: escaping never adds or removes whitespace, so
	// the two always agree, and since this value has no whitespace at all,
	// PowerShell's & operator never wraps it in an outer quote for the
	// trailing backslash to collide with -- so it must stay undoubled.
	// Live-verified end-to-end (2026-08-26, Windows PowerShell 5.1.26100):
	// psQuote(`a"b\`) fed through & to a native argv dumper round-tripped
	// to exactly a"b\.
	got := escapeNativeArg(`a"b\`)
	want := `a\"b\`
	if got != want {
		t.Errorf("escapeNativeArg = %q, want %q", got, want)
	}
}

func TestEscapeNativeArg_PlainArgsUnchanged(t *testing.T) {
	for _, s := range []string{"arg with spaces", "it's quoted"} {
		if got := escapeNativeArg(s); got != s {
			t.Errorf("escapeNativeArg(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestPsQuote_EmbeddedQuoteAndSingleQuoteTogether(t *testing.T) {
	got := psQuote(`it's "quoted"`)
	if !strings.Contains(got, `it''s`) {
		t.Errorf("expected doubled single quote in %q", got)
	}
	if !strings.Contains(got, `\"quoted\"`) {
		t.Errorf("expected escaped double quotes in %q", got)
	}
}

// --- formatStreamValue / extractOutput tests (#239) ---

func TestExtractOutput_MultipleItemsGetNewlineSeparator(t *testing.T) {
	stdout, code := extractOutput([]interface{}{"A", "B", int32(0)})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != "A\nB" {
		t.Errorf("stdout = %q, want %q", stdout, "A\nB")
	}
}

func TestExtractOutput_AlreadyNewlineTerminatedItemsNoBlankLine(t *testing.T) {
	stdout, _ := extractOutput([]interface{}{"A\r\n", "B\r\n", int32(0)})
	want := "A\r\nB\r\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q (no extra blank line)", stdout, want)
	}
}

func TestExtractOutput_TwoLineCommandRoundTrips(t *testing.T) {
	stdout, code := extractOutput([]interface{}{"MARK[a\r\n", "b]\r\n", int32(0)})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	want := "MARK[a\r\nb]\r\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestExtractOutput_EmptyMiddleItemPreservesBlankLine(t *testing.T) {
	// Pins newlineTracker's separator decision against the *original* item
	// text, not the already-prefixed result: an empty item must still
	// require a separator before whatever follows it.
	stdout, _ := extractOutput([]interface{}{"A", "", "B", int32(0)})
	want := "A\n\nB"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// --- newlineTracker tests (#247) ---

func TestNewlineTracker_NoSeparatorBeforeFirstItem(t *testing.T) {
	tracker := &newlineTracker{}
	if got := tracker.next("A"); got != "A" {
		t.Errorf("next(%q) = %q, want %q (no leading separator)", "A", got, "A")
	}
}

func TestNewlineTracker_InsertsSeparatorWhenPreviousLacksTrailingNewline(t *testing.T) {
	tracker := &newlineTracker{}
	tracker.next("A")
	if got := tracker.next("B"); got != "\nB" {
		t.Errorf("next(%q) = %q, want %q", "B", got, "\nB")
	}
}

func TestNewlineTracker_NoSeparatorWhenPreviousEndsInNewline(t *testing.T) {
	tracker := &newlineTracker{}
	tracker.next("A\r\n")
	if got := tracker.next("B"); got != "B" {
		t.Errorf("next(%q) = %q, want %q (no separator)", "B", got, "B")
	}
}

func TestNewlineTracker_EmptyItemStillRequiresSeparatorAfterIt(t *testing.T) {
	tracker := &newlineTracker{}
	tracker.next("A")
	tracker.next("")
	if got := tracker.next("B"); got != "\nB" {
		t.Errorf("next(%q) = %q, want %q", "B", got, "\nB")
	}
}

func TestNewlineTracker_InstancesAreIndependent(t *testing.T) {
	a := &newlineTracker{}
	b := &newlineTracker{}
	a.next("A\r\n") // ends in newline
	b.next("A")     // does not
	if got := a.next("X"); got != "X" {
		t.Errorf("tracker a: next(%q) = %q, want %q", "X", got, "X")
	}
	if got := b.next("X"); got != "\nX" {
		t.Errorf("tracker b: next(%q) = %q, want %q", "X", got, "\nX")
	}
}

// --- streamEmitter tests (#247) ---
//
// streamEmitter covers ExecStream's shipped per-value formatting, exit-
// marker handling, and separator logic without needing a constructible
// *psrpclient.StreamResult -- see ADR-0008's noted ExecStream coverage gap.

type fakeSink struct {
	events []eventstream.Event
}

func (f *fakeSink) Send(_ context.Context, event eventstream.Event) error {
	f.events = append(f.events, event)
	return nil
}

func (f *fakeSink) concatenated(channel eventstream.Channel) string {
	var sb strings.Builder
	for _, e := range f.events {
		if e.Channel == channel {
			sb.Write(e.Payload)
		}
	}
	return sb.String()
}

type erroringSink struct {
	err error
}

func (s *erroringSink) Send(_ context.Context, _ eventstream.Event) error {
	return s.err
}

func TestStreamEmitter_MultiLineStdoutGetsSeparators(t *testing.T) {
	sink := &fakeSink{}
	emitter := newStreamEmitter(sink)

	if err := emitter.emit(context.Background(), "stdout", []interface{}{"Image Name", "PID", "tasklist.exe"}); err != nil {
		t.Fatalf("emit returned error: %v", err)
	}

	got := sink.concatenated("stdout")
	want := "Image Name\nPID\ntasklist.exe"
	if got != want {
		t.Errorf("concatenated stdout = %q, want %q", got, want)
	}
}

func TestStreamEmitter_ChannelsTrackSeparatorsIndependently(t *testing.T) {
	sink := &fakeSink{}
	emitter := newStreamEmitter(sink)

	if err := emitter.emit(context.Background(), "stdout", []interface{}{"out1", "out2"}); err != nil {
		t.Fatalf("emit stdout returned error: %v", err)
	}
	if err := emitter.emit(context.Background(), "stderr", []interface{}{"err1", "err2"}); err != nil {
		t.Fatalf("emit stderr returned error: %v", err)
	}

	if got := sink.concatenated("stdout"); got != "out1\nout2" {
		t.Errorf("concatenated stdout = %q, want %q", got, "out1\nout2")
	}
	if got := sink.concatenated("stderr"); got != "err1\nerr2" {
		t.Errorf("concatenated stderr = %q, want %q", got, "err1\nerr2")
	}
}

func TestStreamEmitter_ExitMarkerIsRecordedNotSent(t *testing.T) {
	sink := &fakeSink{}
	emitter := newStreamEmitter(sink)

	if err := emitter.emit(context.Background(), "stdout", []interface{}{"A", "__BOXY_EXIT_CODE:17"}); err != nil {
		t.Fatalf("emit returned error: %v", err)
	}

	if emitter.exitCode != 17 {
		t.Errorf("exitCode = %d, want 17", emitter.exitCode)
	}
	if got := sink.concatenated("stdout"); got != "A" {
		t.Errorf("concatenated stdout = %q, want %q (exit marker must not be sent)", got, "A")
	}
}

func TestStreamEmitter_DropsEmptyPSObjectWithoutBreakingSeparators(t *testing.T) {
	sink := &fakeSink{}
	emitter := newStreamEmitter(sink)

	values := []interface{}{"A", &serialization.PSObject{}, "B"}
	if err := emitter.emit(context.Background(), "stdout", values); err != nil {
		t.Fatalf("emit returned error: %v", err)
	}

	if got := sink.concatenated("stdout"); got != "A\nB" {
		t.Errorf("concatenated stdout = %q, want %q", got, "A\nB")
	}
}

func TestStreamEmitter_AlreadyNewlineTerminatedItemsMatchExtractOutput(t *testing.T) {
	// Pins the invariant #247 is about: streamEmitter's live per-value path
	// and extractOutput's buffered path must treat equivalent input the same.
	sink := &fakeSink{}
	emitter := newStreamEmitter(sink)
	values := []interface{}{"A\r\n", "B\r\n"}

	if err := emitter.emit(context.Background(), "stdout", values); err != nil {
		t.Fatalf("emit returned error: %v", err)
	}

	wantStdout, _ := extractOutput(values)
	if got := sink.concatenated("stdout"); got != wantStdout {
		t.Errorf("streamEmitter stdout = %q, want %q (extractOutput's result)", got, wantStdout)
	}
}

func TestStreamEmitter_PropagatesSendError(t *testing.T) {
	wantErr := fmt.Errorf("sink closed")
	emitter := newStreamEmitter(&erroringSink{err: wantErr})

	err := emitter.emit(context.Background(), "stdout", []interface{}{"A"})
	if err != wantErr {
		t.Errorf("emit error = %v, want %v", err, wantErr)
	}
}

type stringerStub struct{ s string }

func (s stringerStub) String() string { return s.s }

func TestFormatStreamValue_HonorsStringer(t *testing.T) {
	text, drop := formatStreamValue(stringerStub{s: "hello"})
	if drop {
		t.Fatal("expected drop = false")
	}
	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
}

func TestFormatStreamValue_DropsEmptyPSObject(t *testing.T) {
	_, drop := formatStreamValue(&serialization.PSObject{})
	if !drop {
		t.Error("expected an all-empty PSObject to be dropped")
	}
}

func TestFormatStreamValue_UnwrapsPopulatedPSObject(t *testing.T) {
	text, drop := formatStreamValue(&serialization.PSObject{ToString: "hello"})
	if drop {
		t.Fatal("expected drop = false for a populated PSObject")
	}
	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
}

func TestFormatStreamValue_UnwrapsExceptionMessageEvenWithoutToString(t *testing.T) {
	// Regression test for a design correction found while planning this fix:
	// dropping based on ToString/Value/TypeNames alone would silently
	// discard a real ErrorRecord message reachable only through PSObject's
	// deeper String() fallback (Properties["Exception"]["Message"]).
	obj := &serialization.PSObject{
		Properties: map[string]interface{}{
			"Exception": &serialization.PSObject{
				Properties: map[string]interface{}{
					"Message": "boom",
				},
			},
		},
	}
	text, drop := formatStreamValue(obj)
	if drop {
		t.Fatal("expected drop = false for a PSObject with a real exception message")
	}
	if text != "boom" {
		t.Errorf("text = %q, want %q", text, "boom")
	}
}

func TestFormatStreamValue_DropsNil(t *testing.T) {
	_, drop := formatStreamValue(nil)
	if !drop {
		t.Error("expected nil to be dropped")
	}
}

// --- operationTimeout / wrapKnownTransportError tests (#242) ---
//
// The 30s idle-read cap a caller used to hit unconditionally (go-psrpcore's
// outofproc.Adapter.Read) is now disabled for HvSocket via the forked
// go-psrp/go-psrpcore dependencies this module uses (see go.mod's replace
// directives and AGENTS.md's "PSRP Transport Dependency Fork" section) --
// the cap defers entirely to ctx now. What's tested here is what's testable
// from this package without a live guest: (1) cfg.Timeout derives from the
// caller's real request deadline instead of a hardcoded value, and (2) the
// error wrap for the (now defensive-only) known transport-error string
// still names what happened instead of leaking an opaque vendor error, in
// case that string is ever hit again (e.g. an unforked build).

func TestOperationTimeout_UsesRemainingCtxDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	got := operationTimeout(ctx)
	if got <= 0 || got > 90*time.Second {
		t.Fatalf("operationTimeout = %v, want a positive duration <= 90s", got)
	}
	if got < 80*time.Second {
		t.Errorf("operationTimeout = %v, want close to the 90s deadline (test overhead aside)", got)
	}
}

func TestOperationTimeout_FallsBackWithoutDeadline(t *testing.T) {
	got := operationTimeout(context.Background())
	if got != defaultOperationTimeout {
		t.Errorf("operationTimeout = %v, want fallback %v", got, defaultOperationTimeout)
	}
}

func TestOperationTimeout_FallsBackWhenDeadlineAlreadyPassed(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	got := operationTimeout(ctx)
	if got != defaultOperationTimeout {
		t.Errorf("operationTimeout = %v, want fallback %v for an already-expired deadline", got, defaultOperationTimeout)
	}
}

func TestWrapKnownTransportError_ExplainsFixedIdleCap(t *testing.T) {
	original := fmt.Errorf("runspace pool broken: read fragment header: read timeout: no data received in 30s")
	wrapped := wrapKnownTransportError(original)
	if wrapped == original { //nolint:errorlint // intentional identity check: must be a new wrapping error
		t.Fatal("expected wrapKnownTransportError to wrap a known transport error")
	}
	if !strings.Contains(wrapped.Error(), "independent of --timeout") {
		t.Errorf("wrapped error = %q, want it to explain the cap is independent of --timeout", wrapped.Error())
	}
	if !errors.Is(wrapped, original) {
		t.Error("expected wrapped error to still satisfy errors.Is against the original")
	}
}

func TestWrapKnownTransportError_PassesThroughOtherErrors(t *testing.T) {
	original := fmt.Errorf("connection refused")
	if got := wrapKnownTransportError(original); got != original { //nolint:errorlint // intentional identity check
		t.Errorf("wrapKnownTransportError modified an unrelated error: got %q", got.Error())
	}
}

func TestWrapKnownTransportError_NilPassesThrough(t *testing.T) {
	if got := wrapKnownTransportError(nil); got != nil {
		t.Errorf("wrapKnownTransportError(nil) = %v, want nil", got)
	}
}
