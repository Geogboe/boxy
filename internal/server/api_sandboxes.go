package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// handleListSandboxes returns all sandboxes as JSON.
func (s *Server) handleListSandboxes(w http.ResponseWriter, r *http.Request) {
	sbs, err := s.store.ListSandboxes(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to list sandboxes")
		return
	}
	httpjson.Write(w, http.StatusOK, sbs)
}

// handleGetSandbox returns a single sandbox by ID.
func (s *Server) handleGetSandbox(w http.ResponseWriter, r *http.Request) {
	id := model.SandboxID(r.PathValue("id"))
	sb, err := s.store.GetSandbox(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "sandbox not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to get sandbox")
		return
	}
	httpjson.Write(w, http.StatusOK, sb)
}

// createSandboxRequest is the request body for POST /api/v1/sandboxes.
type createSandboxRequest struct {
	Name     string                `json:"name"`
	Policies model.SandboxPolicies `json:"policies,omitempty"`
}

// handleCreateSandbox creates a new sandbox and returns it with 201.
func (s *Server) handleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	var req createSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sb, err := s.sandboxMgr.Create(r.Context(), req.Name, req.Policies)
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to create sandbox")
		return
	}
	httpjson.Write(w, http.StatusCreated, sb)
}

// handleDeleteSandbox deletes a sandbox by ID and returns 204.
func (s *Server) handleDeleteSandbox(w http.ResponseWriter, r *http.Request) {
	id := model.SandboxID(r.PathValue("id"))
	err := s.store.DeleteSandbox(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpjson.Error(w, http.StatusNotFound, "sandbox not found")
		return
	}
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to delete sandbox")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
