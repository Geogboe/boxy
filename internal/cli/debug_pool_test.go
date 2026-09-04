package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/model"
)

func newDebugPoolTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/pools/{name}/drain", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "missing" {
			http.Error(w, `{"error":"pool not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := printJSONTo(w, model.Pool{Name: model.PoolName(name), Drain: model.PoolDrainState{Operator: true}}); err != nil {
			t.Fatalf("encode pool: %v", err)
		}
	})
	mux.HandleFunc("POST /api/v1/pools/{name}/fill", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "config-drained" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprint(w, `{"error":"pool \"config-drained\" is configured drained; edit config before filling it"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := printJSONTo(w, model.Pool{Name: model.PoolName(name)}); err != nil {
			t.Fatalf("encode pool: %v", err)
		}
	})
	return httptest.NewServer(mux)
}

func TestDebugPoolDrain_success(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"debug", "pool", "--server", srv.URL, "drain", "web"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "drained pool web") {
		t.Fatalf("output = %q, want drain success", output)
	}
}

func TestDebugPoolFill_success(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"debug", "pool", "--server", srv.URL, "fill", "web"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "filled pool web") {
		t.Fatalf("output = %q, want fill success", output)
	}
}

func TestDebugPoolDrain_error(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"debug", "pool", "--server", srv.URL, "drain", "missing"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("execute error = nil, want missing pool")
	}
	if !strings.Contains(err.Error(), "pool not found") {
		t.Fatalf("error = %v, want pool not found", err)
	}
}

func TestDebugPoolDrain_whitespacePoolRejectedBeforeRequest(t *testing.T) {
	hit := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { hit = true })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"debug", "pool", "--server", srv.URL, "drain", "   "})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error for whitespace-only pool name")
	}
	if hit {
		t.Fatal("expected no HTTP request for an invalid pool name")
	}
}

func TestDebugPoolFill_configDeclaredDrain(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"debug", "pool", "--server", srv.URL, "fill", "config-drained"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("execute error = nil, want config drain")
	}
	if !strings.Contains(err.Error(), "configured drained") {
		t.Fatalf("error = %v, want configured drained message", err)
	}
}

func TestAdminPoolDrain_success(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "pool", "--server", srv.URL, "drain", "web"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "drained pool web") {
		t.Fatalf("output = %q, want admin pool drain success", output)
	}
}

func TestAdminPoolDown_alias(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "pool", "--server", srv.URL, "down", "web"})
	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "drained pool web") {
		t.Fatalf("output = %q, want down alias success", output)
	}
}

func TestAdminPoolUp_alias(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "pool", "--server", srv.URL, "up", "web"})
	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(output, "filled pool web") {
		t.Fatalf("output = %q, want up alias success", output)
	}
}
