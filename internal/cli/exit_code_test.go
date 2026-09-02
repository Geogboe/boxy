package cli

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSandboxExecBufferedPreservesGuestExitCode(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sandboxes/sb-1/exec" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"running"}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-1" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"failed","chunks":[{"cursor":"cursor-1","stream":"stdout","data":"b3V0cHV0Cg=="}],"exit_code":7}`)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--buffered", "--", "false"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute succeeded, want typed guest exit error")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 {
		t.Fatalf("error = %v, typed exit error = %+v, want code 7", err, exitErr)
	}
}

func TestSandboxExecStreamingPreservesGuestExitCode(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sandboxes/sb-1/exec" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"running"}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-1" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"failed","chunks":[{"cursor":"cursor-1","stream":"stdout","data":"b3V0cHV0Cg=="}],"exit_code":23}`)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetOut(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--", "false"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute succeeded, want typed guest exit error")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 23 {
		t.Fatalf("error = %v, typed exit error = %+v, want code 23", err, exitErr)
	}
}
