package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestSandboxExecCommandDefaultsToLiveText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sandboxes/sb-1/exec" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"running"}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-1" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"succeeded","chunks":[{"cursor":"cursor-1","stream":"stdout","data":"aGVsbG8K"}],"exit_code":0}`)
			return
		}
		t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--", "echo", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "hello\n" {
		t.Fatalf("stdout = %q, want hello newline", got)
	}
}

type synchronizedOutput struct {
	mu        sync.Mutex
	value     strings.Builder
	firstSeen chan struct{}
	once      sync.Once
}

func (w *synchronizedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.value.Write(p)
	if strings.Contains(w.value.String(), "first chunk\n") {
		w.once.Do(func() { close(w.firstSeen) })
	}
	return len(p), nil
}

func (w *synchronizedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.value.String()
}

func TestSandboxExecCommandLiveTextArrivesBeforeCompletion(t *testing.T) {
	firstSeen := make(chan struct{})
	release := make(chan struct{})
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
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("from") == "" {
				_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"running","chunks":[{"cursor":"cursor-1","stream":"stdout","data":"Zmlyc3QgY2h1bmsK"}],"next":"cursor-1"}`)
				return
			}
			select {
			case <-release:
			case <-time.After(2 * time.Second):
				t.Error("client did not consume the first chunk before completion")
			}
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"succeeded","exit_code":0}`)
			return
		}
		t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}))
	defer server.Close()

	out := &synchronizedOutput{firstSeen: firstSeen}
	cmd := newSandboxCommand()
	cmd.SetOut(out)
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--", "long-running"})

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case <-firstSeen:
		close(release)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("live CLI output did not arrive before completion")
	}
	if err := <-done; err != nil {
		t.Fatalf("Execute: %v; output=%q", err, out.String())
	}
	if got := out.String(); got != "first chunk\n" {
		t.Fatalf("stdout = %q, want first chunk newline", got)
	}
}

func TestSandboxExecCommandEventsEmitsNDJSON(t *testing.T) {
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
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"succeeded","chunks":[{"cursor":"cursor-1","stream":"stdout","data":"aGVsbG8K"}],"exit_code":0}`)
			return
		}
		t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--events", "--", "echo", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); !strings.Contains(got, `"type":"data"`) || !strings.Contains(got, `"type":"complete"`) {
		t.Fatalf("events output = %q, want data and complete records", got)
	}
}

func TestSandboxExecCommandBufferedModeRequestsJSON(t *testing.T) {
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
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"succeeded","chunks":[{"cursor":"cursor-1","stream":"stdout","data":"aGVsbG8K"}],"exit_code":0}`)
			return
		}
		t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	out := new(strings.Builder)
	cmd.SetOut(out)
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--buffered", "--", "echo", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "hello\n" {
		t.Fatalf("buffered stdout = %q, want hello newline", got)
	}
}

func TestSandboxExecCommandTextPreservesOpaquePayload(t *testing.T) {
	var received sandboxExecRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sandboxes/sb-1/exec" {
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Fatalf("decode execution request: %v", err)
			}
			_, _ = io.WriteString(w, `{"exec_id":"exec-opaque","status":"running"}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-opaque" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-opaque","status":"succeeded","exit_code":0}`)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	const opaque = "Write-Output \"a b\"\r\nWrite-Error 'diagnostic'"
	cmd := newSandboxCommand()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--command", opaque})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if received.CommandText != opaque {
		t.Fatalf("command_text = %q, want opaque payload %q", received.CommandText, opaque)
	}
	if len(received.Command) != 0 || received.Script != nil {
		t.Fatalf("opaque command reconstructed into another input form: %+v", received)
	}
}

func TestSandboxExecCommandStdinPreservesMultilinePayload(t *testing.T) {
	var received sandboxExecRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sandboxes/sb-1/exec" {
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Fatalf("decode execution request: %v", err)
			}
			_, _ = io.WriteString(w, `{"exec_id":"exec-stdin","status":"succeeded"}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-stdin" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-stdin","status":"succeeded","exit_code":0}`)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	const opaque = "line one\r\nline two\nline three"
	cmd := newSandboxCommand()
	cmd.SetIn(strings.NewReader(opaque))
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--stdin"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if received.CommandText != opaque {
		t.Fatalf("stdin command_text = %q, want %q", received.CommandText, opaque)
	}
}

func TestSandboxExecCommandDetachPrintsExecutionIDWithoutTailing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sandboxes/sb-1/exec" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-detached","status":"running"}`)
			return
		}
		t.Fatalf("detach must not tail execution: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	out := new(strings.Builder)
	cmd := newSandboxCommand()
	cmd.SetOut(out)
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--detach", "--command", "echo detached"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.String() != "exec-detached\n" {
		t.Fatalf("detach output = %q, want execution ID", out.String())
	}
}

func TestSandboxExecCommandAttachUsesExistingExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-existing" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-existing","status":"succeeded","chunks":[{"cursor":"cursor-1","stream":"stdout","data":"YXR0YWNoZWQK"}],"exit_code":0}`)
			return
		}
		t.Fatalf("attach must only read the existing execution: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	out := new(strings.Builder)
	cmd := newSandboxCommand()
	cmd.SetOut(out)
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--attach", "exec-existing"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.String() != "attached\n" {
		t.Fatalf("attach output = %q, want attached newline", out.String())
	}
}

func TestSandboxExecCommandRejectsConflictingOutputModes(t *testing.T) {
	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"exec", "sb-1", "--events", "--buffered", "--", "echo", "hello"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("Execute error = %v, want conflicting mode error", err)
	}
}

func TestSandboxExecCommandRejectsStreamWithoutCompletion(t *testing.T) {
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
			_, _ = io.WriteString(w, "not json")
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--", "interrupted"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "decode execution status") {
		t.Fatalf("Execute error = %v, want malformed status error", err)
	}
}

func TestSandboxExecCommandPassesGuestPasswordFromStdin(t *testing.T) {
	previous, hadPrevious := os.LookupEnv("BOXY_GUEST_PASSWORD")
	_ = os.Unsetenv("BOXY_GUEST_PASSWORD")
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv("BOXY_GUEST_PASSWORD", previous)
		} else {
			_ = os.Unsetenv("BOXY_GUEST_PASSWORD")
		}
	})

	var request sandboxExecRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-1" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"succeeded","exit_code":0}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"running"}`)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetIn(strings.NewReader("rotated-password\n"))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--guest-password-stdin", "--buffered", "--", "whoami"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if request.GuestCredential == nil || request.GuestCredential.Kind != "password" || string(request.GuestCredential.Data) != `{"password":"rotated-password"}` {
		t.Fatalf("guest credential = %+v, want password payload", request.GuestCredential)
	}
}

func TestSandboxExecCommandRequiresCommand(t *testing.T) {
	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"exec", "sb-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("exec without command succeeded, want error")
	}
}

func TestSandboxExecCommandScriptFileSendsRawBytesAndArgs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.ps1")
	content := []byte("Write-Output 'raw; script'\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var request sandboxExecRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-1" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"succeeded","exit_code":0}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"running"}`)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--script-file", path, "--interpreter", "powershell", "--buffered", "--", "--mode", "ci", "quoted value"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if request.Command != nil || request.Script == nil {
		t.Fatalf("request = %+v, want script-only request", request)
	}
	if string(request.Script.Content) != string(content) {
		t.Fatalf("script content = %q, want raw content", request.Script.Content)
	}
	if request.Script.Interpreter != providersdk.ScriptInterpreterPowerShell {
		t.Fatalf("interpreter = %q, want powershell", request.Script.Interpreter)
	}
	if got := strings.Join(request.Script.Args, "\x00"); got != "--mode\x00ci\x00quoted value" {
		t.Fatalf("script args = %q, want array-preserved args", got)
	}
	if err := request.Script.VerifyDigest(); err != nil {
		t.Fatalf("digest: %v", err)
	}
}

func TestSandboxExecCommandAtScriptFileUsesAutoInterpreter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.sh")
	if err := os.WriteFile(path, []byte("printf '%s\\n' \"$1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var request sandboxExecRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, `{"resource_id":"res-1","exit_code":0}`)
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--buffered", "--", "@" + path, "ci mode"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if request.Script == nil || request.Script.Interpreter != providersdk.ScriptInterpreterAuto {
		t.Fatalf("request script = %+v, want auto interpreter", request.Script)
	}
	if len(request.Script.Args) != 1 || request.Script.Args[0] != "ci mode" {
		t.Fatalf("script args = %#v, want one array element", request.Script.Args)
	}
}

func TestSandboxExecCommandRejectsScriptAndCommandFormsTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.sh")
	if err := os.WriteFile(path, []byte("true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"exec", "sb-1", "--script-file", path, "--", "@other.sh"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("Execute error = %v, want script form conflict", err)
	}
}

func TestSandboxExecCommandLoadsSavedGuestCredential(t *testing.T) {
	backend := &sandboxExecFakeBackend{values: make(map[string]string)}
	store := credentials.NewWithBackend("test", backend)
	credential := providersdk.GuestCredential{Kind: "password", Data: json.RawMessage(`{"password":"saved"}`)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1/exec/exec-1" {
			_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"succeeded","exit_code":0}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sandboxes/sb-1" {
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		var request sandboxExecRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.GuestCredential == nil || string(request.GuestCredential.Data) != `{"password":"saved"}` {
			t.Fatalf("guest credential = %+v, want saved credential", request.GuestCredential)
		}
		_, _ = io.WriteString(w, `{"exec_id":"exec-1","status":"running"}`)
	}))
	defer server.Close()
	if err := store.SetGuestCredential(server.URL, "sb-1", "res-1", credential); err != nil {
		t.Fatalf("SetGuestCredential: %v", err)
	}

	previousStore := guestCredentialStore
	guestCredentialStore = func() *credentials.Store { return store }
	t.Cleanup(func() { guestCredentialStore = previousStore })

	cmd := newSandboxCommand()
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--buffered", "--", "whoami"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

type sandboxExecFakeBackend struct {
	values map[string]string
}

func (b *sandboxExecFakeBackend) Get(service, user string) (string, error) {
	value, ok := b.values[service+"\x00"+user]
	if !ok {
		return "", credentials.ErrNotFound
	}
	return value, nil
}

func (b *sandboxExecFakeBackend) Set(service, user, value string) error {
	b.values[service+"\x00"+user] = value
	return nil
}

func (b *sandboxExecFakeBackend) Delete(service, user string) error {
	delete(b.values, service+"\x00"+user)
	return nil
}
