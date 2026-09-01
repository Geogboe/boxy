package server

import (
	"context"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/diagnostics"
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

// OIDCAuthedRequest seeds st with a valid model.SessionKindOIDC session for
// subject/role and attaches its cookie to r -- for tests exercising
// session-kind-dependent behavior (e.g. profile-page personal-key minting's
// "oidc:<subject>" vs. "local:<username>" split) that AuthedRequest's fixed
// local-admin session can't cover.
func OIDCAuthedRequest(r *http.Request, st store.Store, subject string, role model.APIKeyRole) *http.Request {
	const cookieValue = "test-oidc-session-cookie-value"
	now := time.Now()
	_ = st.PutSession(context.Background(), model.Session{
		ID:        model.SessionID("test-oidc-session-" + subject),
		Hash:      auth.HashSessionToken(cookieValue),
		Kind:      model.SessionKindOIDC,
		Subject:   subject,
		Role:      role,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	})
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieValue})
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

// NewTestMuxWithCatalog is NewTestMux with a supplied read-only catalog
// source, for testing catalog rendering and authorization boundaries.
func NewTestMuxWithCatalog(st store.Store, sm *sandbox.Manager, source CatalogSource, uiEnabled bool) *http.ServeMux {
	if uiEnabled {
		seedTestSession(st)
	}
	s := &Server{store: st, sandboxMgr: sm, catalog: source, uiEnabled: uiEnabled}
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

// NewTestMuxWithSessionTTL is NewTestMux (no pre-seeded session) plus a
// configured session TTL, for tests verifying server.oidc.session_ttl
// actually reaches a freshly minted session's ExpiresAt.
func NewTestMuxWithSessionTTL(st store.Store, sm *sandbox.Manager, ttl time.Duration) *http.ServeMux {
	s := &Server{
		store:      st,
		sandboxMgr: sm,
		uiEnabled:  true,
		sessionTTL: ttl,
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

// NewTestMuxWithDiagnostics configures the diagnostics REST and UI surfaces.
// authRequired controls whether API requests must carry a bearer key; UI
// requests continue to use the seeded session when uiEnabled is true.
func NewTestMuxWithDiagnostics(st store.Store, sm *sandbox.Manager, logs diagnostics.Store, audit diagnostics.AuditSink, uiEnabled, authRequired bool) *http.ServeMux {
	if uiEnabled {
		seedTestSession(st)
	}
	s := &Server{
		store:        st,
		sandboxMgr:   sm,
		diagnostics:  logs,
		audit:        audit,
		uiEnabled:    uiEnabled,
		authRequired: authRequired,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

// NewTestMuxWithResourceCleanup configures the administrator resource purge
// endpoint for API tests.
func NewTestMuxWithResourceCleanup(st store.Store, sm *sandbox.Manager, cleanup ResourceCleanup) *http.ServeMux {
	return newTestMuxWithResourceCleanup(st, sm, cleanup, false)
}

// NewTestMuxWithResourceCleanupAuth is the authenticated variant used to
// exercise API-key role checks around resource cleanup.
func NewTestMuxWithResourceCleanupAuth(st store.Store, sm *sandbox.Manager, cleanup ResourceCleanup) *http.ServeMux {
	return newTestMuxWithResourceCleanup(st, sm, cleanup, true)
}

// NewTestMuxWithPoolAdmin configures the authenticated Pools dashboard and its
// operator action seams.
func NewTestMuxWithPoolAdmin(st store.Store, sm *sandbox.Manager, maintenance PoolMaintenance, cleanup ResourceCleanup) *http.ServeMux {
	seedTestSession(st)
	s := &Server{store: st, sandboxMgr: sm, poolMaintenance: maintenance, resourceCleanup: cleanup, uiEnabled: true}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

func newTestMuxWithResourceCleanup(st store.Store, sm *sandbox.Manager, cleanup ResourceCleanup, authRequired bool) *http.ServeMux {
	s := &Server{store: st, sandboxMgr: sm, resourceCleanup: cleanup}
	s.authRequired = authRequired
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}
