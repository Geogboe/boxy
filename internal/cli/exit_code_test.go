package cli

import (
	"encoding/base64"
	"encoding/json"
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
		if r.URL.Path != "/api/v1/sandboxes/sb-1/exec" {
			t.Fatalf("path = %q, want buffered exec path", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"resource_id":"res-1","stdout":"output\n","exit_code":7}`)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--", "false"})
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
		if r.URL.Query().Get("stream") != "true" {
			t.Fatalf("query = %q, want stream=true", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		data, _ := json.Marshal(map[string]any{
			"type": "data", "stream": "stdout", "data": base64.StdEncoding.EncodeToString([]byte("output\n")),
		})
		_, _ = w.Write(append(data, '\n'))
		complete, _ := json.Marshal(map[string]any{"type": "complete", "exit_code": 23})
		_, _ = w.Write(append(complete, '\n'))
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetOut(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--stream", "--", "false"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute succeeded, want typed guest exit error")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 23 {
		t.Fatalf("error = %v, typed exit error = %+v, want code 23", err, exitErr)
	}
}
