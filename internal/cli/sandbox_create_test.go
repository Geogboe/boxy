package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

type sandboxCreateTestServer struct {
	server *httptest.Server

	mu                    sync.Mutex
	pools                 []model.Pool
	createStatus          int
	createErrorMessage    string
	createBody            string
	createdSandbox        model.Sandbox
	sandboxStates         []model.Sandbox
	resources             map[string]model.Resource
	createCalls           int
	getSandboxCalls       int
	getResourceCalls      int
	guestCredentialStatus int
	guestCredentials      []sandboxGuestCredentialDelivery
	guestCredentialCalls  int
}

func newSandboxCreateTestServer(t *testing.T) *sandboxCreateTestServer {
	t.Helper()

	ts := &sandboxCreateTestServer{
		pools: []model.Pool{
			{
				Name: "web",
				Inventory: model.ResourceCollection{
					ExpectedType:    model.ResourceTypeContainer,
					ExpectedProfile: "web",
				},
			},
		},
		createStatus:       http.StatusAccepted,
		createErrorMessage: "sandbox requests are required",
		createdSandbox: model.Sandbox{
			ID:     "sb-create",
			Name:   "lab",
			Status: model.SandboxStatusPending,
			Requests: []model.ResourceRequest{{
				Type:    model.ResourceTypeContainer,
				Profile: "web",
				Count:   1,
			}},
		},
		sandboxStates: []model.Sandbox{
			{
				ID:     "sb-create",
				Name:   "lab",
				Status: model.SandboxStatusPending,
				Requests: []model.ResourceRequest{{
					Type:    model.ResourceTypeContainer,
					Profile: "web",
					Count:   1,
				}},
			},
			{
				ID:     "sb-create",
				Name:   "lab",
				Status: model.SandboxStatusReady,
				Requests: []model.ResourceRequest{{
					Type:    model.ResourceTypeContainer,
					Profile: "web",
					Count:   1,
				}},
				Resources: []model.ResourceID{"res-1"},
			},
		},
		resources: map[string]model.Resource{
			"res-1": {
				ID:      "res-1",
				Type:    model.ResourceTypeContainer,
				Profile: "web",
				Properties: map[string]any{
					"host": "127.0.0.1",
					"port": 2222,
				},
			},
		},
		guestCredentialStatus: http.StatusGone,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ts.pools)
	})
	mux.HandleFunc("POST /api/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.createCalls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, _ := json.Marshal(body)
		ts.createBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ts.createStatus)
		if ts.createStatus >= http.StatusBadRequest {
			_, _ = fmt.Fprintf(w, `{"error":"%s"}`, ts.createErrorMessage)
			return
		}
		_ = json.NewEncoder(w).Encode(ts.createdSandbox)
	})
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.getSandboxCalls++
		w.Header().Set("Content-Type", "application/json")
		if len(ts.sandboxStates) == 0 {
			_ = json.NewEncoder(w).Encode(ts.createdSandbox)
			return
		}
		state := ts.sandboxStates[0]
		if len(ts.sandboxStates) > 1 {
			ts.sandboxStates = ts.sandboxStates[1:]
		}
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("GET /api/v1/resources/{id}", func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.getResourceCalls++
		id := r.PathValue("id")
		res, ok := ts.resources[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
	mux.HandleFunc("GET /api/v1/sandboxes/{id}/guest-credential", func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.guestCredentialCalls++
		if ts.guestCredentialStatus != http.StatusOK {
			w.WriteHeader(ts.guestCredentialStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sandboxGuestCredentialResponse{Credentials: ts.guestCredentials})
	})

	ts.server = httptest.NewServer(mux)
	return ts
}

func (ts *sandboxCreateTestServer) Close() {
	ts.server.Close()
}

func writeSandboxSpec(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func TestWriteEnvFile_RestoresPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}

	dir := t.TempDir()
	t.Setenv("BOXY_WORKING_DIR", dir)
	path := filepath.Join(dir, ".sandbox-lab.env")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("setup env file: %v", err)
	}

	if err := writeEnvFile(model.Sandbox{ID: "sb-lab", Name: "lab"}, nil, ".sandbox-lab.env"); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("env file permissions = %04o, want %04o", got, want)
	}
}

func TestSandboxCreate_BlockingSuccess(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	defer srv.Close()

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath, "--no-env-file"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, want := range []string{"Connection info", "SANDBOX_ID=sb-create", "SANDBOX_WEB_1_HOST=127.0.0.1", "boxy sandbox get sb-create"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", srv.createCalls)
	}
	if srv.getSandboxCalls == 0 {
		t.Fatal("expected polling to fetch sandbox state")
	}
	if srv.getResourceCalls != 1 {
		t.Fatalf("getResourceCalls = %d, want 1", srv.getResourceCalls)
	}
	if !strings.Contains(srv.createBody, `"profile":"web"`) || !strings.Contains(srv.createBody, `"type":"container"`) {
		t.Fatalf("createBody = %s, want compiled request payload", srv.createBody)
	}
	if strings.Contains(srv.createBody, `"pool":"web"`) {
		t.Fatalf("createBody = %s, did not expect raw pool references in API payload", srv.createBody)
	}
}

func TestSandboxCreate_NoWait(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	defer srv.Close()

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath, "--no-wait", "--no-env-file"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "Sandbox accepted") {
		t.Fatalf("output = %q, want accepted message", output)
	}
	if strings.Contains(output, "Connection info") {
		t.Fatalf("output = %q, did not expect connection info", output)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.getSandboxCalls != 0 {
		t.Fatalf("getSandboxCalls = %d, want 0", srv.getSandboxCalls)
	}
	if srv.getResourceCalls != 0 {
		t.Fatalf("getResourceCalls = %d, want 0", srv.getResourceCalls)
	}
}

func TestSandboxCreate_PrintsGuestCredentialOnce(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	srv.guestCredentialStatus = http.StatusOK
	srv.guestCredentials = []sandboxGuestCredentialDelivery{{
		ResourceID: "res-1",
		Credential: &providersdk.GuestCredential{Kind: "password", Data: json.RawMessage(`{"username":"Administrator","password":"rotated"}`)},
	}}
	defer srv.Close()

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath, "--no-env-file"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "Guest credentials") || !strings.Contains(output, "rotated") {
		t.Fatalf("output = %q, want one-time guest credential", output)
	}
}

func TestSandboxCreate_SavesGuestCredential(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	srv.guestCredentialStatus = http.StatusOK
	srv.guestCredentials = []sandboxGuestCredentialDelivery{{
		ResourceID: "res-1",
		Credential: &providersdk.GuestCredential{Kind: "password", Data: json.RawMessage(`{"password":"rotated"}`)},
	}}
	defer srv.Close()

	backend := &sandboxCreateFakeBackend{values: make(map[string]string)}
	previousStore := guestCredentialStore
	guestCredentialStore = func() *credentials.Store { return credentials.NewWithBackend("test", backend) }
	t.Cleanup(func() { guestCredentialStore = previousStore })

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath, "--save-guest-cred", "--no-env-file"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "Saved guest credential for resource res-1") {
		t.Fatalf("output = %q, want saved confirmation", output)
	}
	if strings.Contains(output, "rotated") {
		t.Fatalf("output = %q, did not expect secret when saving", output)
	}
	stored, err := credentials.NewWithBackend("test", backend).GetGuestCredential(srv.server.URL, "sb-create", "res-1")
	if err != nil {
		t.Fatalf("GetGuestCredential: %v", err)
	}
	if string(stored.Data) != `{"password":"rotated"}` {
		t.Fatalf("stored credential = %+v", stored)
	}
}

func TestSandboxCreate_SaveGuestCredentialRequiresWait(t *testing.T) {
	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", "127.0.0.1:1", "create", "-f", specPath, "--save-guest-cred", "--no-wait"})
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "requires waiting") {
		t.Fatalf("error = %v, want --save-guest-cred wait validation", err)
	}
}

func TestSandboxCreate_UnknownPool(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	defer srv.Close()

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: missing\n    count: 1\n")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `pool "missing" not found on server`) {
		t.Fatalf("error = %v", err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0", srv.createCalls)
	}
}

func TestSandboxCreate_RejectsStrayPositionalArgs(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	defer srv.Close()

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath, "unexpected-arg"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for a stray positional argument to `sandbox create`")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0 (stray arg should be rejected before any request)", srv.createCalls)
	}
}

// TestSandboxCreate_InvalidSpec verifies that early spec-validation failures
// produce the "✗  <step>  —  <error>" output pattern rather than leaving the
// spinner spinning until after the error message has been printed.
func TestSandboxCreate_InvalidSpec(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	defer srv.Close()

	// A spec with no resources should fail validation before hitting the server.
	specPath := writeSandboxSpec(t, "name: test\nresources: []\n")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err == nil {
		t.Fatal("expected error for empty resources")
	}
	if !strings.Contains(err.Error(), "sandbox spec resources is required") {
		t.Fatalf("error = %v", err)
	}
	// The fail output should appear in stdout so the spinner is resolved.
	if !strings.Contains(output, "Loading sandbox spec") {
		t.Fatalf("output = %q, want step label in fail output", output)
	}
	if !strings.Contains(output, "sandbox spec resources is required") {
		t.Fatalf("output = %q, want error detail in fail output", output)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0 (validation should fail before server call)", srv.createCalls)
	}
}

func TestLoadSandboxSpec_RejectsMalformedPackageReference(t *testing.T) {
	specPath := writeSandboxSpec(t, "name: test\nresources:\n  - pool: default\n    count: 1\n    packages: [baseline]\n")

	_, err := loadSandboxSpec(specPath)
	if err == nil || !strings.Contains(err.Error(), "must use name@version") {
		t.Fatalf("loadSandboxSpec() error = %v, want name@version validation error", err)
	}
}

func TestSandboxCreate_FailedStatus(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	srv.sandboxStates = []model.Sandbox{
		{
			ID:     "sb-create",
			Name:   "lab",
			Status: model.SandboxStatusFailed,
			Error:  "pool \"web\" has 0 ready resource(s), need 1",
		},
	}
	defer srv.Close()

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `sandbox "sb-create" failed: pool "web" has 0 ready resource(s), need 1`) {
		t.Fatalf("error = %v", err)
	}
}

func TestSandboxCreate_CreateAPIErrorIncludesServerMessage(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	srv.createStatus = http.StatusBadRequest
	defer srv.Close()

	specPath := writeSandboxSpec(t, "name: lab\nresources:\n  - pool: web\n    count: 1\n")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.server.URL, "create", "-f", specPath})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sandbox requests are required") {
		t.Fatalf("error = %v, want server message", err)
	}
}

func TestWaitForSandboxReady_Interrupted(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForSandboxReady(ctx, defaultAPIClient(), srv.server.URL, model.Sandbox{
		ID:     "sb-create",
		Name:   "lab",
		Status: model.SandboxStatusPending,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `sandbox "sb-create" created but wait was interrupted`) {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitForSandboxReady_ReturnsPollingAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"database unavailable"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := waitForSandboxReady(context.Background(), defaultAPIClient(), srv.URL, model.Sandbox{
		ID:     "sb-create",
		Name:   "lab",
		Status: model.SandboxStatusPending,
	})
	if err == nil {
		t.Fatal("expected polling error")
	}
	if !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("error = %v, want server message", err)
	}
}

func TestHydrateSandboxResourcesSkipsMissingResources(t *testing.T) {
	srv := newSandboxCreateTestServer(t)
	defer srv.Close()

	resources, err := hydrateSandboxResources(context.Background(), defaultAPIClient(), srv.server.URL, model.Sandbox{
		ID:        "sb-create",
		Resources: []model.ResourceID{"res-1", "missing"},
	})
	if err != nil {
		t.Fatalf("hydrateSandboxResources: %v", err)
	}
	if len(resources) != 1 || resources[0].ID != "res-1" {
		t.Fatalf("resources = %+v, want only res-1", resources)
	}
}

type sandboxCreateFakeBackend struct {
	values map[string]string
}

func (b *sandboxCreateFakeBackend) Get(service, user string) (string, error) {
	value, ok := b.values[service+"\x00"+user]
	if !ok {
		return "", credentials.ErrNotFound
	}
	return value, nil
}

func (b *sandboxCreateFakeBackend) Set(service, user, value string) error {
	b.values[service+"\x00"+user] = value
	return nil
}

func (b *sandboxCreateFakeBackend) Delete(service, user string) error {
	delete(b.values, service+"\x00"+user)
	return nil
}
