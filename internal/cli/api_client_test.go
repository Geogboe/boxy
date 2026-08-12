package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/credentials"
)

func TestWrapConnError_ClassifiesDialFailure(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	_, doErr := client.Do(req) //nolint:bodyclose // Do fails before a body exists
	if doErr == nil {
		t.Fatal("expected a dial error connecting to 127.0.0.1:1")
	}

	wrapped := wrapConnError(doErr, "127.0.0.1:1")
	if !strings.Contains(wrapped.Error(), "boxy serve") {
		t.Fatalf("wrapped error = %v, want mention of `boxy serve`", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "127.0.0.1:1") {
		t.Fatalf("wrapped error = %v, want the unreachable address", wrapped)
	}
	if !errors.Is(wrapped, doErr) {
		t.Fatal("wrapped error should still unwrap to the original dial error")
	}
}

func TestWrapConnError_LeavesNonConnErrorsUnchanged(t *testing.T) {
	orig := errors.New("some other failure")
	if got := wrapConnError(orig, "127.0.0.1:9090"); got != orig { //nolint:err113
		t.Fatalf("wrapConnError changed a non-connection error: %v", got)
	}
}

func TestWrapConnError_Nil(t *testing.T) {
	if wrapConnError(nil, "addr") != nil {
		t.Fatal("expected wrapConnError(nil, ...) to return nil")
	}
}

func TestDoNoContent_WrapsDialFailure(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodDelete, "http://127.0.0.1:1/api/v1/agent-tokens/token-1", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	err = doNoContent(client, req)
	if err == nil {
		t.Fatal("expected a dial error connecting to 127.0.0.1:1")
	}
	if !strings.Contains(err.Error(), "boxy serve") {
		t.Fatalf("error = %v, want a friendly `boxy serve` hint", err)
	}
}

func TestValidatePathID_RejectsEmpty(t *testing.T) {
	if _, err := validatePathID("sandbox id", ""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestValidatePathID_RejectsWhitespaceOnly(t *testing.T) {
	if _, err := validatePathID("sandbox id", "   "); err == nil {
		t.Fatal("expected error for whitespace-only id")
	}
}

func TestValidatePathID_TrimsAndEscapesSlashes(t *testing.T) {
	got, err := validatePathID("sandbox id", " sb/1 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sb%2F1" {
		t.Fatalf("got %q, want escaped id sb%%2F1", got)
	}
}

func TestValidatePathID_AcceptsOrdinaryID(t *testing.T) {
	got, err := validatePathID("sandbox id", "sb-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sb-1" {
		t.Fatalf("got %q, want unchanged id", got)
	}
}

func TestAPIClientForServerAttachesStoredCredential(t *testing.T) {
	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	backend := &testCredentialBackend{values: make(map[string]string)}
	creds := credentials.NewWithBackend("boxy", backend)
	if err := creds.Set(srv.URL, "boxy_secret"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}

	client := apiClientWithCredentials(srv.URL, creds, false)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	_ = resp.Body.Close()
	if got, want := gotAuth.Load().(string), "Bearer boxy_secret"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

type testCredentialBackend struct {
	values map[string]string
}

func (b *testCredentialBackend) Get(service, user string) (string, error) {
	return b.values[service+"\x00"+user], nil
}

func (b *testCredentialBackend) Set(service, user, value string) error {
	b.values[service+"\x00"+user] = value
	return nil
}

func (b *testCredentialBackend) Delete(service, user string) error {
	delete(b.values, service+"\x00"+user)
	return nil
}

func TestMaintenanceAPIClientHasABoundedTimeout(t *testing.T) {
	// A hung daemon must not block `debug pool drain/fill` forever: the
	// maintenance client needs a timeout, just a longer one than the default
	// client's since drain/fill can legitimately take longer than a status
	// check. It must also be generous enough to survive a realistic worst
	// case: applyDrain destroys pool resources serially, and the Hyper-V
	// driver's Delete waits up to 30s per VM for a transitional power state
	// to clear before tearing down (see ADR-0004) — a pool with just a
	// handful of VMs stuck mid-transition could exceed a short timeout
	// while the drain is still legitimately in progress server-side.
	if maintenanceAPIClient().Timeout == 0 {
		t.Fatal("maintenance client timeout = 0, want a bounded timeout")
	}
	if maintenanceAPIClient().Timeout <= defaultAPIClient().Timeout {
		t.Fatalf("maintenance client timeout = %v, want it longer than the default client's %v", maintenanceAPIClient().Timeout, defaultAPIClient().Timeout)
	}
	if maintenanceAPIClient().Timeout < 5*time.Minute {
		t.Fatalf("maintenance client timeout = %v, want at least 5m to survive a multi-VM serial drain", maintenanceAPIClient().Timeout)
	}
}

// TestExecAPIClientHasABoundedTimeout guards against the default client's 5s
// http.Client.Timeout being used for `sandbox exec`: that timeout bounds the
// entire request, including reading the response body, so it would bound a
// `--stream` NDJSON response too — not just connection setup. The server
// accepts a per-request timeout up to 5 minutes (internal/server/api_exec.go's
// maxExecTimeout) and defaults to 30s, both well past the default client's
// 5s, so exec needs its own longer-timeout client the same way drain/fill
// already does via maintenanceAPIClient.
// TestApiBaseURL_BareAddressDefaultsToHTTPS guards a GitHub Copilot review
// finding on this PR: a schemeless --server address (e.g. "myhost:9090")
// used to default to http://, even though boxy serve is HTTPS by default —
// so the single most natural way to point the CLI at a remote server
// (bare host:port, no scheme) silently built an HTTP request against a
// TLS-only listener and just failed to connect. Only an explicit http://
// prefix should still get plain HTTP; everything else defaults secure,
// matching the empty-string (fully-default) case below it.
func TestApiBaseURL_BareAddressDefaultsToHTTPS(t *testing.T) {
	if got, want := apiBaseURL("myhost:9090"), "https://myhost:9090"; got != want {
		t.Fatalf("apiBaseURL(bare) = %q, want %q", got, want)
	}
	if got, want := apiBaseURL(""), "https://127.0.0.1:9090"; got != want {
		t.Fatalf("apiBaseURL(empty) = %q, want %q", got, want)
	}
	if got, want := apiBaseURL("http://myhost:9090"), "http://myhost:9090"; got != want {
		t.Fatalf("apiBaseURL(explicit http) = %q, want %q (explicit http:// must still opt into plain HTTP for local --insecure dev)", got, want)
	}
	if got, want := apiBaseURL("https://myhost:9090"), "https://myhost:9090"; got != want {
		t.Fatalf("apiBaseURL(explicit https) = %q, want %q", got, want)
	}
}

func TestExecAPIClientHasABoundedTimeout(t *testing.T) {
	if execAPIClient().Timeout == 0 {
		t.Fatal("exec client timeout = 0, want a bounded timeout")
	}
	if execAPIClient().Timeout <= defaultAPIClient().Timeout {
		t.Fatalf("exec client timeout = %v, want it longer than the default client's %v", execAPIClient().Timeout, defaultAPIClient().Timeout)
	}
	if execAPIClient().Timeout < 5*time.Minute {
		t.Fatalf("exec client timeout = %v, want at least 5m to match the server's maxExecTimeout", execAPIClient().Timeout)
	}
}
