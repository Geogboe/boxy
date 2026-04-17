package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

type sandboxCreateTestServer struct {
	server *httptest.Server

	mu                 sync.Mutex
	pools              []model.Pool
	createStatus       int
	createErrorMessage string
	createBody         string
	createdSandbox     model.Sandbox
	sandboxStates      []model.Sandbox
	resources          map[string]model.Resource
	createCalls        int
	getSandboxCalls    int
	getResourceCalls   int
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
