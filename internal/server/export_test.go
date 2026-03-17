package server

import (
	"net/http"

	"github.com/Geogboe/boxy/pkg/store"
)

// NewTestMux creates a configured http.ServeMux for testing without starting a listener.
func NewTestMux(st store.Store, uiEnabled bool) *http.ServeMux {
	s := &Server{
		store:     st,
		uiEnabled: uiEnabled,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}
