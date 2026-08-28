package server

import (
	"context"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/model"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

// TestSessionCookieValue is the raw session-cookie value NewTestMux and
// NewTestMuxWithAgentAdminUI seed into the given store (as a valid,
// admin-role model.Session) whenever uiEnabled is true, since every UI
// route requires a session now (see session.go). AuthedRequest attaches it
// to a request.
const TestSessionCookieValue = "test-session-cookie-value"

func seedTestSession(st store.Store) {
	now := time.Now()
	_ = st.PutSession(context.Background(), model.Session{
		ID:        "test-session",
		Hash:      auth.HashSessionToken(TestSessionCookieValue),
		Kind:      model.SessionKindLocalAdmin,
		Subject:   "test-admin",
		Role:      model.APIKeyRoleAdmin,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	})
}

// AuthedRequest attaches the session cookie seeded by NewTestMux/
// NewTestMuxWithAgentAdminUI (when uiEnabled) to r, for tests exercising
// session-gated UI routes directly against a test mux (mux.ServeHTTP, not a
// real http.Client with a cookie jar to carry a cookie across requests).
func AuthedRequest(r *http.Request) *http.Request {
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: TestSessionCookieValue})
	return r
}

// NewTestMux creates a configured http.ServeMux for testing without starting a listener.
func NewTestMux(st store.Store, sm *sandbox.Manager, uiEnabled bool, pm ...PoolMaintenance) *http.ServeMux {
	var maintenance PoolMaintenance
	if len(pm) > 0 {
		maintenance = pm[0]
	}
	if uiEnabled {
		seedTestSession(st)
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

// NewTestMuxWithOIDC is NewTestMux plus OIDC login enabled.
func NewTestMuxWithOIDC(st store.Store, sm *sandbox.Manager, oidcOpts *OIDCOptions) *http.ServeMux {
	seedTestSession(st)
	s := &Server{
		store:      st,
		sandboxMgr: sm,
		uiEnabled:  true,
		oidc:       oidcOpts,
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
	if uiEnabled {
		seedTestSession(st)
	}
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
