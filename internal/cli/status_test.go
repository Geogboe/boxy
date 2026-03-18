package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRunStatus_Healthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"test-pool","inventory":{"expected_type":"container","expected_profile":"alpine","resources":[{"id":"r1","type":"container","profile":"alpine"}]}}]`))
	})
	mux.HandleFunc("GET /api/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"sb-1","name":"test"}]`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Capture stderr
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	addr := srv.Listener.Addr().String()
	cmd := newStatusCommand()
	cmd.SetArgs([]string{"--server", addr})
	err := cmd.ExecuteContext(context.Background())

	w.Close()
	os.Stderr = old

	if err != nil {
		t.Fatalf("status error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("healthy")) {
		t.Errorf("expected 'healthy' in output, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("1 configured")) {
		t.Errorf("expected '1 configured' in output, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("1 resources ready")) {
		t.Errorf("expected '1 resources ready' in output, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("1 active")) {
		t.Errorf("expected '1 active' in output, got: %s", output)
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
