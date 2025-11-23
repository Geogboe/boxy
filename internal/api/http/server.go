package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/internal/core/sandbox"
)

// SandboxService captures the subset of sandbox.Manager we need.
type SandboxService interface {
	Create(ctx context.Context, req *sandbox.CreateRequest) (*sandbox.Sandbox, error)
	List(ctx context.Context) ([]*sandbox.Sandbox, error)
	Get(ctx context.Context, id string) (*sandbox.Sandbox, error)
	Destroy(ctx context.Context, id string) error
	Extend(ctx context.Context, id string, d time.Duration) error
	GetResourcesForSandbox(ctx context.Context, sandboxID string) ([]*sandbox.ResourceWithConnection, error)
}

// PoolStatsProvider gives pool information for status endpoints.
type PoolStatsProvider interface {
	List() ([]PoolStatus, error)
}

// Server hosts the HTTP REST API.
type Server struct {
	addr     string
	logger   *logrus.Logger
	sandbox  SandboxService
	pools    PoolStatsProvider
	server   *http.Server
	router   http.Handler
	timeouts Timeouts
}

// Timeouts aggregates server timeout settings.
type Timeouts struct {
	Read  time.Duration
	Write time.Duration
	Idle  time.Duration
}

// NewServer constructs an HTTP API server.
func NewServer(addr string, sandbox SandboxService, pools PoolStatsProvider, logger *logrus.Logger, timeouts Timeouts) *Server {
	s := &Server{
		addr:     addr,
		logger:   logger,
		sandbox:  sandbox,
		pools:    pools,
		timeouts: timeouts,
	}
	s.router = s.routes()
	return s
}

// Handler exposes the HTTP handler (useful for tests).
func (s *Server) Handler() http.Handler {
	return s.router
}

// Run starts serving until ctx is cancelled or the server is closed.
func (s *Server) Run(ctx context.Context) error {
	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      s.router,
		ReadTimeout:  s.timeouts.Read,
		WriteTimeout: s.timeouts.Write,
		IdleTimeout:  s.timeouts.Idle,
	}

	// Graceful shutdown on context cancellation.
	idleConnsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			s.logger.WithError(err).Warn("HTTP server graceful shutdown failed")
		}
		close(idleConnsClosed)
	}()

	s.logger.WithField("addr", s.addr).Info("HTTP API listening")

	err := s.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-idleConnsClosed
		return nil
	}
	return err
}
