package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestServer_healthz(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	srv := server.New(st, sandbox.New(st, nil), ":0", false)
	_ = srv // we test via httptest below

	// Use httptest to avoid binding a real port.
	mux := server.NewTestMux(st, sandbox.New(st, nil), false)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "ok" {
		t.Fatalf("body = %q, want %q", w.Body.String(), "ok")
	}
}

func TestServer_start_shutdown(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	srv := server.New(st, sandbox.New(st, nil), "127.0.0.1:0", false)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Cancel triggers graceful shutdown.
	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}
