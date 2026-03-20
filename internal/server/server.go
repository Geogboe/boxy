// Package server provides the HTTP server that serves both the JSON REST API
// and the optional web dashboard for Boxy.
//
// The server takes a [store.Store] as its data source and exposes read-only
// endpoints for pools, resources, and sandboxes. The web UI (Go templates + HTMX)
// can be toggled via the UIEnabled option.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/store"
)

// Server is the HTTP server for the Boxy REST API and optional web UI.
type Server struct {
	store      store.Store
	sandboxMgr *sandbox.Manager
	uiEnabled  bool
	addr       string
	srv        *http.Server
}

// New creates a Server that will listen on addr.
// If uiEnabled is true, the web dashboard is served at /.
func New(st store.Store, sm *sandbox.Manager, addr string, uiEnabled bool) *Server {
	s := &Server{
		store:      st,
		sandboxMgr: sm,
		uiEnabled:  uiEnabled,
		addr:       addr,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.srv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

// registerRoutes wires all API and UI routes into the mux.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Health check
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// REST API
	s.registerAPIRoutes(mux)

	// Web UI (optional)
	if s.uiEnabled {
		s.registerUIRoutes(mux)
	}
}

// Start begins listening and serving. It blocks until the server is shut down
// or the context is cancelled. Returns nil on graceful shutdown.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	slog.Info("http server listening", "addr", ln.Addr().String())

	// Shut down when context is cancelled.
	go func() {
		<-ctx.Done()
		slog.Info("http server shutting down")
		_ = s.srv.Shutdown(context.Background())
	}()

	if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http serve: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
