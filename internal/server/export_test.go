package server

import (
	"net/http"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/store"
)

// NewTestMux creates a configured http.ServeMux for testing without starting a listener.
func NewTestMux(st store.Store, sm *sandbox.Manager, uiEnabled bool) *http.ServeMux {
	s := &Server{
		store:      st,
		sandboxMgr: sm,
		uiEnabled:  uiEnabled,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}
