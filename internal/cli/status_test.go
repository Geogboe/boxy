package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunStatus_Healthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"name":"test-pool","inventory":{"expected_type":"container","expected_profile":"alpine","resources":[{"id":"r1","type":"container","profile":"alpine"}]}}]`)
	})
	mux.HandleFunc("GET /api/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"id":"sb-1","name":"test"}]`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	addr := srv.Listener.Addr().String()
	cmd := newStatusCommand()
	stdout, stderr := captureCommandOutput(cmd)
	cmd.SetArgs([]string{"--server", addr})
	execErr := cmd.ExecuteContext(context.Background())

	if execErr != nil {
		t.Fatalf("status error: %v", execErr)
	}

	output := stdout.String()

	for _, want := range []string{"healthy", "1 configured", "1 resources ready", "1 active"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got: %s", want, output)
		}
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunStatus_ServerDown(t *testing.T) {
	cmd := newStatusCommand()
	stdout, stderr := captureCommandOutput(cmd)
	cmd.SetArgs([]string{"--server", "127.0.0.1:1"}) // port 1 — nothing there
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error when server is down")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	for _, want := range []string{"cannot reach server", "boxy serve"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestRunStatus_Unhealthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	addr := srv.Listener.Addr().String()
	cmd := newStatusCommand()
	stdout, stderr := captureCommandOutput(cmd)
	cmd.SetArgs([]string{"--server", addr})
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error when server is unhealthy")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); !strings.Contains(got, "unhealthy") {
		t.Fatalf("expected stderr to contain unhealthy, got %q", got)
	}
}

func captureCommandOutput(cmd *cobra.Command) (*bytes.Buffer, *bytes.Buffer) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return &stdout, &stderr
}
