package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/jobs"
)

// newDebugPoolTestServer fakes the real async job protocol (#328): POST
// .../drain and .../fill always answer 202 Accepted with an already-terminal
// jobs.Job (see internal/server/api_pools.go's submitPoolMaintenanceJob for
// the real, asynchronous shape) so runPoolMaintenanceJob's waitForJob loop
// resolves on its first check without needing to poll GET /api/v1/jobs/{id}.
func newDebugPoolTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	terminalJob := func(kind, poolName string, status jobs.Status, errorCode string) jobs.Job {
		return jobs.Job{ID: jobs.ID(kind + "-" + poolName), Kind: kind, Target: "pool:" + poolName, Status: status, ErrorCode: errorCode, CreatedAt: time.Now().UTC()}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/pools/{name}/drain", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var job jobs.Job
		switch name {
		case "missing":
			job = terminalJob("pool.drain", name, jobs.StatusFailed, "pool_operation_failed")
		default:
			job = terminalJob("pool.drain", name, jobs.StatusSucceeded, "")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if err := printJSONTo(w, job); err != nil {
			t.Fatalf("encode job: %v", err)
		}
	})
	mux.HandleFunc("POST /api/v1/pools/{name}/fill", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var job jobs.Job
		switch name {
		case "config-drained":
			job = terminalJob("pool.fill", name, jobs.StatusFailed, "pool_config_drained")
		case "quarantine-exhausted":
			job = terminalJob("pool.fill", name, jobs.StatusFailed, "quarantine_exhausted")
		default:
			job = terminalJob("pool.fill", name, jobs.StatusSucceeded, "")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if err := printJSONTo(w, job); err != nil {
			t.Fatalf("encode job: %v", err)
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
		t.Fatal("execute error = nil, want a failed job reported")
	}
	if !strings.Contains(err.Error(), "pool_operation_failed") {
		t.Fatalf("error = %v, want pool_operation_failed", err)
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

// TestDebugPoolFill_quarantineExhausted covers #328: a fill that made no
// progress purely because quarantined resources consume the pool's entire
// max_total must be unambiguous -- a non-zero exit and an explicit message,
// never a silent "filled pool" success.
func TestDebugPoolFill_quarantineExhausted(t *testing.T) {
	srv := newDebugPoolTestServer(t)
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"debug", "pool", "--server", srv.URL, "fill", "quarantine-exhausted"})

	output, err := captureSandboxStdout(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err == nil {
		t.Fatal("execute error = nil, want quarantine-exhausted failure")
	}
	if strings.Contains(output, "filled pool") {
		t.Fatalf("output = %q, must not report success", output)
	}
	if !strings.Contains(err.Error(), "quarantine") {
		t.Fatalf("error = %v, want a quarantine-exhausted explanation", err)
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
