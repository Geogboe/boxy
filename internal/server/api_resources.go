package server

import (
	"errors"
	"net/http"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// handleListResources returns all resources as JSON.
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	res, err := s.store.ListResources(r.Context())
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "failed to list resources")
		return
	}
	httpjson.Write(w, http.StatusOK, res)
}

// handleGetResource returns a single resource by ID.
func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
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
