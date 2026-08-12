package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSandboxExecCommandStreamsNDJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sandboxes/sb-1/exec" || r.URL.Query().Get("stream") != "true" {
			t.Fatalf("request = %s?%s, want streaming exec path", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		data, _ := json.Marshal(map[string]any{
			"type":   "data",
			"stream": "stdout",
			"data":   base64.StdEncoding.EncodeToString([]byte("hello\n")),
		})
		_, _ = w.Write(append(data, '\n'))
		complete, _ := json.Marshal(map[string]any{"type": "complete", "exit_code": 0})
		_, _ = w.Write(append(complete, '\n'))
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--stream", "--", "echo", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "hello\n" {
		t.Fatalf("stdout = %q, want hello newline", got)
	}
}

func TestSandboxExecCommandRequiresCommand(t *testing.T) {
	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"exec", "sb-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("exec without command succeeded, want error")
	}
}
