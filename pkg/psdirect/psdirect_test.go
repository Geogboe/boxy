package psdirect

import (
	"context"
	"fmt"
	"strings"
	"testing"

	psrpclient "github.com/smnsjas/go-psrp/client"
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
	e := New("test-guid", "admin", "pass")
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
	e := New("test-guid", "admin", "pass")
	e.execFactory = func() (psrpExecutor, error) {
		return nil, fmt.Errorf("client creation failed")
	}

	_, err := e.Exec(context.Background(), "echo")
	if err == nil {
		t.Fatal("expected error when execFactory fails")
	}
}

func TestExec_New(t *testing.T) {
	e := New("guid-123", "user", "pass")
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
