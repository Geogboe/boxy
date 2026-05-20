package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/pterm/pterm"
)

func captureSandboxStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	pterm.SetDefaultOutput(w)
	pterm.DisableStyling()

	runErr := fn()

	_ = w.Close()
	os.Stdout = old
	pterm.SetDefaultOutput(old)
	pterm.EnableStyling()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}

	return buf.String(), runErr
}

func newSandboxTestServer(t *testing.T, listItems []model.Sandbox) *httptest.Server {
	t.Helper()

	resourceOne := model.Resource{
		ID:       "res-1",
		Type:     model.ResourceTypeContainer,
		Profile:  "ubuntu-2204",
		State:    model.ResourceStateAllocated,
		Provider: model.ProviderRef{Name: "docker"},
		Properties: map[string]any{
			"host": "127.0.0.1",
			"port": 2222,
		},
		CreatedAt: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 27, 12, 5, 0, 0, time.UTC),
	}
	resourceTwo := model.Resource{
		ID:       "res-2",
		Type:     model.ResourceTypeVM,
		Profile:  "win-2022",
		State:    model.ResourceStateAllocated,
		Provider: model.ProviderRef{Name: "hyperv"},
	}

	sandboxes := map[string]model.Sandbox{
		"sb-1": {ID: "sb-1", Name: "one", Status: model.SandboxStatusPending, Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "ubuntu-2204", Count: 1}}, Resources: []model.ResourceID{"res-1"}},
		"sb-2": {ID: "sb-2", Name: "two", Status: model.SandboxStatusReady, Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "ubuntu-2204", Count: 1}, {Type: model.ResourceTypeVM, Profile: "win-2022", Count: 1}}, Resources: []model.ResourceID{"res-1", "res-2"}},
	}
	resources := map[string]model.Resource{
		"res-1": resourceOne,
		"res-2": resourceTwo,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if len(listItems) == 0 {
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		if err := printJSONTo(w, listItems); err != nil {
			t.Fatalf("encode sandboxes: %v", err)
		}
	})
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		sb, ok := sandboxes[id]
		if !ok {
			http.Error(w, `{"error":"sandbox not found"}`, http.StatusNotFound)
			return
		}
		if id == "sb-2" {
			sb.Policies = model.SandboxPolicies{AutoDestroyAfter: "8h", SecurityProfile: "lab"}
		}
		if err := printJSONTo(w, sb); err != nil {
			t.Fatalf("encode sandbox: %v", err)
		}
	})
	mux.HandleFunc("GET /api/v1/resources/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		res, ok := resources[id]
		if !ok {
			http.Error(w, `{"error":"resource not found"}`, http.StatusNotFound)
			return
		}
		if err := printJSONTo(w, res); err != nil {
			t.Fatalf("encode resource: %v", err)
		}
	})
	mux.HandleFunc("DELETE /api/v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		sb, ok := sandboxes[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		sb.Status = model.SandboxStatusDeleting
		sandboxes[id] = sb
		delete(sandboxes, id)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if err := printJSONTo(w, sb); err != nil {
			t.Fatalf("encode sandbox delete: %v", err)
		}
	})

	return httptest.NewServer(mux)
}

func printJSONTo(w http.ResponseWriter, v any) error {
	return json.NewEncoder(w).Encode(v)
}

func TestSandboxList_empty(t *testing.T) {
	srv := newSandboxTestServer(t, nil)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.URL, "list"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "No sandboxes found.") {
		t.Fatalf("output = %q, want no sandboxes message", output)
	}
}

func TestSandboxList_with_sandboxes(t *testing.T) {
	srv := newSandboxTestServer(t, []model.Sandbox{
		{ID: "sb-1", Name: "one", Status: model.SandboxStatusPending, Resources: []model.ResourceID{"res-1"}},
		{ID: "sb-2", Name: "two", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1", "res-2"}},
	})
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.URL, "list"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"sb-1", "one", "pending", "sb-2", "two", "ready", "2"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
}

func TestSandboxGet_found(t *testing.T) {
	srv := newSandboxTestServer(t, []model.Sandbox{
		{ID: "sb-1", Name: "one", Status: model.SandboxStatusPending, Resources: []model.ResourceID{"res-1"}},
		{ID: "sb-2", Name: "two", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1", "res-2"}},
	})
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.URL, "get", "sb-2"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{`"id": "sb-2"`, `"name": "two"`, `"status": "ready"`, `"security_profile": "lab"`, `"profile": "ubuntu-2204"`, `"id": "res-1"`, `"id": "res-2"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %q", output, want)
		}
	}
}

func TestSandboxGet_not_found(t *testing.T) {
	srv := newSandboxTestServer(t, []model.Sandbox{
		{ID: "sb-1", Name: "one", Status: model.SandboxStatusPending, Resources: []model.ResourceID{"res-1"}},
		{ID: "sb-2", Name: "two", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1", "res-2"}},
	})
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.URL, "get", "no-such-id"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for non-existent sandbox")
	}
	if !strings.Contains(err.Error(), `sandbox "no-such-id" not found`) {
		t.Fatalf("error = %v", err)
	}
}

func TestSandboxDelete_found(t *testing.T) {
	srv := newSandboxTestServer(t, []model.Sandbox{
		{ID: "sb-1", Name: "one", Status: model.SandboxStatusPending, Resources: []model.ResourceID{"res-1"}},
		{ID: "sb-2", Name: "two", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1", "res-2"}},
	})
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.URL, "delete", "sb-1"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(output, "deleted sandbox sb-1") {
		t.Fatalf("output = %q", output)
	}
}

func TestSandboxDelete_NoWaitReturnsAfterAccepted(t *testing.T) {
	srv := newSandboxTestServer(t, []model.Sandbox{
		{ID: "sb-1", Name: "one", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1"}},
	})
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.URL, "delete", "sb-1", "--no-wait"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "accepted deletion of sandbox sb-1") {
		t.Fatalf("output = %q", output)
	}
}

func TestSandboxDelete_not_found(t *testing.T) {
	srv := newSandboxTestServer(t, []model.Sandbox{
		{ID: "sb-1", Name: "one", Status: model.SandboxStatusPending, Resources: []model.ResourceID{"res-1"}},
		{ID: "sb-2", Name: "two", Status: model.SandboxStatusReady, Resources: []model.ResourceID{"res-1", "res-2"}},
	})
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"sandbox", "--server", srv.URL, "delete", "no-such-id"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for non-existent sandbox")
	}
	if !strings.Contains(err.Error(), `sandbox "no-such-id" not found`) {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitForSandboxDeleted_ReturnsNilOnNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := waitForSandboxDeleted(context.Background(), defaultAPIClient(), srv.URL, "sb-1"); err != nil {
		t.Fatalf("waitForSandboxDeleted: %v", err)
	}
}

func TestWaitForSandboxDeleted_ReturnsServerErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"cleanup status unavailable"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := waitForSandboxDeleted(context.Background(), defaultAPIClient(), srv.URL, "sb-1")
	if err == nil {
		t.Fatal("expected wait error")
	}
	if !strings.Contains(err.Error(), "cleanup status unavailable") {
		t.Fatalf("error = %v, want server message", err)
	}
}

func TestWaitForSandboxDeleted_InterruptedWhileSandboxStillExists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = printJSONTo(w, model.Sandbox{ID: "sb-1", Status: model.SandboxStatusDeleting})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForSandboxDeleted(ctx, defaultAPIClient(), srv.URL, "sb-1")
	if err == nil {
		t.Fatal("expected interrupted wait error")
	}
	if !strings.Contains(err.Error(), "deletion accepted but wait was interrupted") {
		t.Fatalf("error = %v, want interrupted message", err)
	}
}
