package server

import (
	"net/http"

	"github.com/Geogboe/boxy/pkg/httpjson"
)

// registerAPIRoutes wires the JSON REST API endpoints into the mux.
func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/pools", s.handleListPools)
	mux.HandleFunc("GET /api/v1/resources", s.handleListResources)
	mux.HandleFunc("GET /api/v1/sandboxes", s.handleListSandboxes)
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
