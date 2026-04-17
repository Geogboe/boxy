package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
	Name     string                  `json:"name"`
	Policies model.SandboxPolicies   `json:"policies,omitempty"`
	Requests []model.ResourceRequest `json:"requests"`
}

// handleCreateSandbox creates a new sandbox request and returns it with 202.
func (s *Server) handleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	var req createSandboxRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpjson.Error(w, http.StatusBadRequest, "sandbox name is required")
		return
	}
	if len(req.Requests) == 0 {
		httpjson.Error(w, http.StatusBadRequest, "sandbox requests are required")
		return
	}
	for _, request := range req.Requests {
		if err := request.Validate(); err != nil {
			httpjson.Error(w, http.StatusBadRequest, "invalid sandbox request: "+err.Error())
			return
		}
	}

	sb, err := s.sandboxMgr.CreateRequested(r.Context(), req.Name, req.Policies, req.Requests)
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to create sandbox")
		return
	}
	httpjson.Write(w, http.StatusAccepted, sb)
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
