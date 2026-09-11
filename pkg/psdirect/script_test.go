package psdirect

import (
	"context"
	"strings"
	"testing"

	psrpclient "github.com/smnsjas/go-psrp/client"
)

func TestSessionExecScriptPreservesStructuredArguments(t *testing.T) {
	argument := "${BOXY_TEST_PASSWORD} ' ; & ` \" trailing\\"
	mock := &mockExecutor{execCommandFunc: func(_ context.Context, script string, isScript bool, args ...interface{}) (*psrpclient.Result, error) {
		if !isScript || strings.Contains(script, argument) || len(args) != 1 || args[0] != argument {
			t.Fatal("script argument was rewritten or embedded in source")
		}
		return &psrpclient.Result{Output: []interface{}{"done", scriptCompletion}}, nil
	}}
	session := &Session{executor: mock}
	result, err := session.ExecScript(context.Background(), "param($value)\n$value", argument)
	if err != nil || result.ExitCode != 0 || result.Stdout != "done" {
		t.Fatalf("unexpected script result: %#v, %v", result, err)
	}
	if mock.connectCount != 0 || mock.closeCount != 0 {
		t.Fatal("script changed session ownership")
	}
}

func TestSessionExecScriptRequiresCompletion(t *testing.T) {
	for _, output := range [][]interface{}{nil, {int32(0)}, {"partial output"}} {
		mock := &mockExecutor{execCommandFunc: func(context.Context, string, bool, ...interface{}) (*psrpclient.Result, error) {
			return &psrpclient.Result{Output: output}, nil
		}}
		if _, err := (&Session{executor: mock}).ExecScript(context.Background(), "throw 'failure'"); err == nil {
			t.Fatal("incomplete script accepted as successful")
		}
	}
}

func TestSessionExecScriptRejectsErrorRecords(t *testing.T) {
	for _, result := range []*psrpclient.Result{nil, {Output: []interface{}{scriptCompletion}, HadErrors: true}, {Output: []interface{}{scriptCompletion}, Errors: []interface{}{"${BOXY_TEST_PASSWORD}"}}} {
		mock := &mockExecutor{execCommandFunc: func(context.Context, string, bool, ...interface{}) (*psrpclient.Result, error) { return result, nil }}
		_, err := (&Session{executor: mock}).ExecScript(context.Background(), "Write-Error 'failure'")
		if err == nil || strings.Contains(err.Error(), "${BOXY_TEST_PASSWORD}") {
			t.Fatal("error records accepted or exposed")
		}
	}
}
