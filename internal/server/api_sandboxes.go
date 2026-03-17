package server

import (
	"net/http"

	"github.com/Geogboe/boxy/v2/pkg/httpjson"
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
