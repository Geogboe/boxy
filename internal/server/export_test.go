package server

import (
	"net/http"

	"github.com/Geogboe/boxy/internal/sandbox"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

// NewTestMux creates a configured http.ServeMux for testing without starting a listener.
func NewTestMux(st store.Store, sm *sandbox.Manager, uiEnabled bool, pm ...PoolMaintenance) *http.ServeMux {
	var maintenance PoolMaintenance
	if len(pm) > 0 {
		maintenance = pm[0]
	}
	s := &Server{
		store:           st,
		sandboxMgr:      sm,
		poolMaintenance: maintenance,
		uiEnabled:       uiEnabled,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

// NewTestMuxWithGuestSecrets configures the pool credential endpoint with an
// explicit server-owned secret store.
func NewTestMuxWithGuestSecrets(st store.Store, sm *sandbox.Manager, secrets boxysecrets.Store) *http.ServeMux {
	s := &Server{store: st, sandboxMgr: sm, guestSecrets: secrets}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

// NewTestMuxWithAgentAdmin is NewTestMux plus an AgentAdmin, for testing
// the /api/v1/agents endpoints.
func NewTestMuxWithAgentAdmin(st store.Store, sm *sandbox.Manager, aa AgentAdmin) *http.ServeMux {
	return NewTestMuxWithAgentAdminUI(st, sm, aa, false)
}

// NewTestMuxWithAgentAdminUI is NewTestMuxWithAgentAdmin with an explicit UI
// toggle, for testing pages that render agent data.
func NewTestMuxWithAgentAdminUI(st store.Store, sm *sandbox.Manager, aa AgentAdmin, uiEnabled bool) *http.ServeMux {
	s := &Server{
		store:      st,
		sandboxMgr: sm,
		agentAdmin: aa,
		uiEnabled:  uiEnabled,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}
