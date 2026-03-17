package server

import (
	"net/http"

	"github.com/Geogboe/boxy/v2/pkg/httpjson"
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
