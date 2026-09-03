// Package server provides the HTTP server that serves both the JSON REST API
// and the optional web dashboard for Boxy.
//
// The server takes a [store.Store] as its data source and exposes the Boxy REST
// API for pools, resources, and sandboxes. Sandbox creation/deletion is served
// here, while the web UI (Go templates + HTMX) can be toggled via the UIEnabled
// option.
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Geogboe/boxy/internal/buildcfg"
	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

// PoolMaintenance performs operator pool maintenance actions for API handlers.
type PoolMaintenance interface {
	Drain(ctx context.Context, poolName model.PoolName) (model.Pool, error)
	Fill(ctx context.Context, poolName model.PoolName) (model.Pool, error)
}

// ResourceCleanup performs the shared, confirmation-protected resource purge
// workflow used by the REST API and web dashboard.
type ResourceCleanup interface {
	Purge(ctx context.Context, request pool.CleanupRequest) (pool.CleanupReport, error)
}

// AgentAdmin exposes operator actions against the agent transport for API
// handlers — a narrow seam (same pattern as PoolMaintenance) implemented by
// internal/agentserver.Server.
type AgentAdmin interface {
	ListAgents() []pool.AgentSummary
	Revoke(ctx context.Context, agentID, reason string, forceOrphanResources bool) error
}

// SandboxExecutor is the application seam used by the REST exec endpoint.
// Implementations own provider-specific operation construction and agent
// routing; the server owns request validation and HTTP event encoding.
type SandboxExecutor interface {
	ExecuteSandbox(ctx context.Context, resource model.Resource, operation providersdk.ExecOperation, sink eventstream.Sink) (*providersdk.Result, error)
}

// Server is the HTTP server for the Boxy REST API and optional web UI.
type Server struct {
	store           store.Store
	sandboxMgr      *sandbox.Manager
	poolMaintenance PoolMaintenance
	resourceCleanup ResourceCleanup
	agentAdmin      AgentAdmin
	executor        SandboxExecutor
	executions      *executionManager
	executionMu     sync.Mutex
	guestSecrets    boxysecrets.Store
	catalog         CatalogSource
	diagnostics     diagnostics.Store
	audit           diagnostics.AuditSink
	oidc            *OIDCOptions
	sessionTTL      time.Duration
	uiEnabled       bool
	authRequired    bool
	insecureHTTP    bool
	version         string
	repositoryURL   string
	tlsCertPEM      []byte
	tlsKeyPEM       []byte
	addr            string
	srv             *http.Server
}

// ServerOptions controls transport security and API authentication.
type ServerOptions struct {
	AuthRequired    bool
	InsecureHTTP    bool
	TLSCertPEM      []byte
	TLSKeyPEM       []byte
	Executor        SandboxExecutor
	ResourceCleanup ResourceCleanup
	GuestSecrets    boxysecrets.Store
	// Catalog is an immutable, startup-time view of configured templates,
	// packages, sources, stores, and pool relationships for the UI.
	Catalog CatalogSource
	// Diagnostics is the bounded, redacted operational log store. When nil,
	// the diagnostics endpoint reports that diagnostics are unavailable.
	Diagnostics diagnostics.Store
	// DiagnosticsAudit receives safe metadata for every diagnostics query.
	DiagnosticsAudit diagnostics.AuditSink
	// OIDC enables provider login on the web UI's /login page. nil (the
	// default) means only the bootstrapped local admin account can log
	// in -- see docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md.
	OIDC *OIDCOptions
	// SessionTTL bounds how long a web-UI login session lasts, regardless
	// of how it was established (OIDC or the local-admin account). Zero
	// defaults to 12h (see session.go's defaultSessionTTL).
	SessionTTL time.Duration
	// Version is displayed in the dashboard footer. Empty defaults to dev.
	Version string
}

// New creates a Server that will listen on addr. It retains the in-process,
// unauthenticated HTTP behavior used by tests and embedded callers; the
// daemon uses NewWithOptions to enable authenticated TLS by default.
// If uiEnabled is true, the web dashboard is served at /.
func New(st store.Store, sm *sandbox.Manager, pm PoolMaintenance, aa AgentAdmin, addr string, uiEnabled bool) *Server {
	return NewWithOptions(st, sm, pm, aa, addr, uiEnabled, ServerOptions{InsecureHTTP: true})
}

// NewWithOptions creates a daemon-configured HTTP server.
func NewWithOptions(st store.Store, sm *sandbox.Manager, pm PoolMaintenance, aa AgentAdmin, addr string, uiEnabled bool, opts ServerOptions) *Server {
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	s := &Server{
		store:           st,
		sandboxMgr:      sm,
		poolMaintenance: pm,
		resourceCleanup: opts.ResourceCleanup,
		agentAdmin:      aa,
		executor:        opts.Executor,
		guestSecrets:    opts.GuestSecrets,
		catalog:         opts.Catalog,
		diagnostics:     opts.Diagnostics,
		audit:           opts.DiagnosticsAudit,
		oidc:            opts.OIDC,
		sessionTTL:      opts.SessionTTL,
		uiEnabled:       uiEnabled,
		authRequired:    opts.AuthRequired,
		insecureHTTP:    opts.InsecureHTTP,
		version:         version,
		repositoryURL:   "https://github.com/" + buildcfg.Repo,
		tlsCertPEM:      append([]byte(nil), opts.TLSCertPEM...),
		tlsKeyPEM:       append([]byte(nil), opts.TLSKeyPEM...),
		addr:            addr,
	}
	s.executions = newExecutionManager(st, opts.Executor)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
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
	apiMux := http.NewServeMux()
	s.registerAPIRoutes(apiMux)
	if s.authRequired {
		mux.Handle("/api/v1/", s.authenticate(apiMux))
	} else {
		mux.Handle("/api/v1/", apiMux)
	}

	// GET /auth/cli-config is registered unconditionally (not gated on
	// s.uiEnabled the way the rest of /auth/* and /login are): `boxy login
	// --oidc` needs it even on a daemon running with the web dashboard
	// disabled, and its own handler already 404s cleanly when OIDC/
	// CLIClientID isn't configured. Registering it only when configured
	// would instead let it fall through to the UI's session-gated catch-all
	// route below whenever uiEnabled is true, which redirects to /login and
	// hands the CLI's JSON decoder an HTML page instead of a clear error.
	mux.HandleFunc("GET /auth/cli-config", s.handleCLIOIDCConfig)

	// Web UI (optional). Always behind a session — a bootstrapped local
	// admin account covers the case where no OIDC provider is configured
	// (see docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md
	// Decision 1); there is no "open dashboard" mode.
	if s.uiEnabled {
		mux.Handle("GET /static/", s.staticHandler())
		mux.HandleFunc("GET /login", s.handleLoginPage)
		mux.HandleFunc("POST /login", s.handleLoginSubmit)
		mux.HandleFunc("POST /logout", s.handleLogout)
		if s.oidc != nil {
			mux.HandleFunc("GET /auth/login", s.handleOIDCLogin)
			mux.HandleFunc("GET /auth/callback", s.handleOIDCCallback)
		}

		protected := http.NewServeMux()
		s.registerUIRoutes(protected)
		mux.Handle("/", s.requireSession(protected))
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
	go func() { //nolint:gosec // intentional use of background context for shutdown grace period
		<-ctx.Done()
		slog.Info("http server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()

	if s.insecureHTTP || len(s.tlsCertPEM) == 0 || len(s.tlsKeyPEM) == 0 {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	}

	cert, err := tls.X509KeyPair(s.tlsCertPEM, s.tlsKeyPEM)
	if err != nil {
		return fmt.Errorf("load HTTP server certificate: %w", err)
	}
	s.srv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	if err := s.srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("https serve: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.executions != nil {
		s.executions.shutdown()
	}
	return s.srv.Shutdown(ctx)
}
