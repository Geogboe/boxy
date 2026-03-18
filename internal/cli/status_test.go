package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRunStatus_Healthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"name":"test-pool","inventory":{"expected_type":"container","expected_profile":"alpine","resources":[{"id":"r1","type":"container","profile":"alpine"}]}}]`)
	})
	mux.HandleFunc("GET /api/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"sb-1","name":"test"}]`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Capture stderr
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	addr := srv.Listener.Addr().String()
	cmd := newStatusCommand()
	cmd.SetArgs([]string{"--server", addr})
	execErr := cmd.ExecuteContext(context.Background())

	_ = w.Close()
	os.Stderr = old

	if execErr != nil {
		t.Fatalf("status error: %v", execErr)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	output := buf.String()

	for _, want := range []string{"healthy", "1 configured", "1 resources ready", "1 active"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got: %s", want, output)
		}
	}
}

func TestRunStatus_ServerDown(t *testing.T) {
	cmd := newStatusCommand()
	cmd.SetArgs([]string{"--server", "127.0.0.1:1"}) // port 1 — nothing there
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error when server is down")
	}
}
