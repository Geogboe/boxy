package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// captureStderr mirrors captureStdout (version_test.go) but for os.Stderr.
// printCurlIfEnabled writes there directly (matching setupLogging's own
// stderr-by-default precedent) rather than through cmd.ErrOrStderr(), so a
// true end-to-end wiring test has to intercept the real os.Stderr, not a
// cobra-injected writer.
func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() {
		_ = r.Close()
	}()

	os.Stderr = w
	defer func() {
		os.Stderr = old
	}()

	runErr := fn()
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String(), runErr
}

// TestPrintCurlFlag_WiresThroughToRealRequests is an end-to-end check that
// --print-curl, set once on the root command, actually reaches every
// request `boxy status` makes (checkHealth, then two fetchJSON calls) via
// PersistentPreRunE's context propagation (root.go) down through doJSON and
// the hand-rolled checkHealth call site (status.go) -- not just that the
// lower-level helpers work in isolation.
func TestPrintCurlFlag_WiresThroughToRealRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/pools":
			_, _ = w.Write([]byte("[]"))
		case "/api/v1/sandboxes":
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"status", "--server", srv.URL, "--print-curl"})
	cmd.SetOut(&bytes.Buffer{})

	stderr, err := captureStderr(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, want := range []string{
		"curl -X GET '" + srv.URL + "/healthz'",
		"curl -X GET '" + srv.URL + "/api/v1/pools'",
		"curl -X GET '" + srv.URL + "/api/v1/sandboxes'",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

// TestPrintCurlFlag_OffByDefault confirms the flag is opt-in: without it, a
// normal command run emits no curl lines to stderr.
func TestPrintCurlFlag_OffByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/pools", "/api/v1/sandboxes":
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"status", "--server", srv.URL})
	cmd.SetOut(&bytes.Buffer{})

	stderr, err := captureStderr(t, func() error {
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(stderr, "curl -X") {
		t.Errorf("stderr = %q, want no curl output when --print-curl is not set", stderr)
	}
}
