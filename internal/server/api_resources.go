package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type resourcePurgeRequest struct {
	DryRun *bool `json:"dry_run"`
	Force  bool  `json:"force"`
}

// handlePurgeResources previews or performs the administrator-only cleanup
// workflow. Omitting dry_run is intentionally a preview; mutation requires
// the explicit force confirmation.
func (s *Server) handlePurgeResources(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAdmin) {
		return
	}
	if s.resourceCleanup == nil {
		httpjson.Error(w, http.StatusServiceUnavailable, "resource cleanup is not available")
		return
	}
	var req resourcePurgeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	if !dryRun && !req.Force {
		httpjson.Error(w, http.StatusBadRequest, "resource cleanup mutation requires force=true")
		return
	}
	principal := principalFromRequest(r)
	actor := string(principal.KeyID)
	if actor == "" {
		actor = principal.Subject
	}
	if actor == "" {
		actor = "local-admin"
	}
	report, err := s.resourceCleanup.Purge(r.Context(), pool.CleanupRequest{
		Actor: actor, DryRun: dryRun, Force: req.Force,
	})
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "resource cleanup failed")
		return
	}
	httpjson.Write(w, http.StatusOK, report)
}

// handleListResources returns all resources as JSON.
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAuditor, model.APIKeyRoleAdmin) {
		return
	}
	res, err := s.store.ListResources(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to list resources")
		return
	}
	httpjson.Write(w, http.StatusOK, res)
}

// handleGetResource returns a single resource by ID.
func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, model.APIKeyRoleAuditor, model.APIKeyRoleAdmin) {
		return
	}
	id := model.ResourceID(r.PathValue("id"))
	res, err := s.store.GetResource(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "resource not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get resource")
		return
	}
	httpjson.Write(w, http.StatusOK, res)
}
