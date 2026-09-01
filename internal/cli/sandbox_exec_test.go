package cli

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"sb-1","resources":["res-1"]}`)
			return
		}
		if r.URL.Path != "/api/v1/sandboxes/sb-1/exec" || r.URL.Query().Get("stream") != "" {
			t.Fatalf("request = %s?%s, want default streaming exec path", r.URL.Path, r.URL.RawQuery)
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
		if r.URL.Path != "/api/v1/sandboxes/sb-1/exec" || r.URL.Query().Get("stream") != "" {
			t.Fatalf("request = %s?%s, want default streaming exec path", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test response does not support flushing")
		}
		data, _ := json.Marshal(map[string]any{"type": "data", "stream": "stdout", "data": base64.StdEncoding.EncodeToString([]byte("first chunk\n"))})
		_, _ = w.Write(append(data, '\n'))
		flusher.Flush()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
			t.Error("client did not consume the first chunk before completion")
		}
		complete, _ := json.Marshal(map[string]any{"type": "complete", "exit_code": 0})
		_, _ = w.Write(append(complete, '\n'))
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
		if r.URL.Path != "/api/v1/sandboxes/sb-1/exec" || r.URL.Query().Get("stream") != "" {
			t.Fatalf("request = %s?%s, want default streaming exec path", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		data, _ := json.Marshal(map[string]any{"type": "data", "stream": "stdout", "data": base64.StdEncoding.EncodeToString([]byte("hello\n"))})
		_, _ = w.Write(append(data, '\n'))
		complete, _ := json.Marshal(map[string]any{"type": "complete", "exit_code": 0})
		_, _ = w.Write(append(complete, '\n'))
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
		if r.URL.Path != "/api/v1/sandboxes/sb-1/exec" || r.URL.Query().Get("stream") != "false" {
			t.Fatalf("request = %s?%s, want stream=false", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"resource_id":"res-1","stdout":"hello\n","exit_code":0}`)
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
		w.Header().Set("Content-Type", "application/x-ndjson")
		data, _ := json.Marshal(map[string]any{"type": "data", "stream": "stdout", "data": base64.StdEncoding.EncodeToString([]byte("partial\n"))})
		_, _ = w.Write(append(data, '\n'))
	}))
	defer server.Close()

	cmd := newSandboxCommand()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--server", server.URL, "exec", "sb-1", "--", "interrupted"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "complete event") {
		t.Fatalf("Execute error = %v, want missing-completion error", err)
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
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = io.WriteString(w, `{"resource_id":"res-1","exit_code":0}`)
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

func TestSandboxExecCommandLoadsSavedGuestCredential(t *testing.T) {
	backend := &sandboxExecFakeBackend{values: make(map[string]string)}
	store := credentials.NewWithBackend("test", backend)
	credential := providersdk.GuestCredential{Kind: "password", Data: json.RawMessage(`{"password":"saved"}`)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_, _ = io.WriteString(w, `{"resource_id":"res-1","exit_code":0}`)
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
