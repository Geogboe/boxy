package server

import (
	"errors"
	"net/http"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// registerAPIRoutes wires the JSON REST API endpoints into the mux.
func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/pools", s.handleListPools)
	mux.HandleFunc("GET /api/v1/pools/{name}", s.handleGetPool)
	mux.HandleFunc("GET /api/v1/resources", s.handleListResources)
	mux.HandleFunc("GET /api/v1/resources/{id}", s.handleGetResource)
	mux.HandleFunc("GET /api/v1/sandboxes", s.handleListSandboxes)
	mux.HandleFunc("GET /api/v1/sandboxes/{id}", s.handleGetSandbox)
	mux.HandleFunc("POST /api/v1/sandboxes", s.handleCreateSandbox)
	mux.HandleFunc("DELETE /api/v1/sandboxes/{id}", s.handleDeleteSandbox)
}

// handleListPools returns all pools as JSON.
func (s *Server) handleListPools(w http.ResponseWriter, r *http.Request) {
	pools, err := s.store.ListPools(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to list pools")
		return
	}
	httpjson.Write(w, http.StatusOK, pools)
}

// handleGetPool returns a single pool by name.
func (s *Server) handleGetPool(w http.ResponseWriter, r *http.Request) {
	name := model.PoolName(r.PathValue("name"))
	pool, err := s.store.GetPool(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "pool not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get pool")
		return
	}
	httpjson.Write(w, http.StatusOK, pool)
}
