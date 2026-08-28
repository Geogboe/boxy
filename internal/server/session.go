package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

// sessionCookieName is the web-UI login session cookie. Deliberately
// distinct from any REST bearer-token concept — this cookie only ever
// authenticates browser requests to UI routes, never /api/v1/*.
const sessionCookieName = "boxy_session"

// defaultSessionTTL is how long a freshly created session is valid before
// the browser is redirected back to /login, when server.oidc.session_ttl
// is unset. Server.sessionTTL carries the configured value (see
// effectiveSessionTTL); this applies uniformly to local-admin and OIDC
// sessions alike, since both share the same session mechanism.
const defaultSessionTTL = 12 * time.Hour

// effectiveSessionTTL returns s.sessionTTL, falling back to
// defaultSessionTTL when unset -- covers both NewWithOptions callers that
// never set ServerOptions.SessionTTL (test muxes, embedded callers) and a
// zero config.OIDCSpec.SessionTTL (EffectiveSessionTTL already defaults
// that to 12h, but a zero-value Server built directly in a test bypasses
// that path entirely).
func (s *Server) effectiveSessionTTL() time.Duration {
	if s.sessionTTL > 0 {
		return s.sessionTTL
	}
	return defaultSessionTTL
}

// No separate CSRF token exists for POST /login, POST /logout, or POST
// /ui/profile/personal-key (profile.go's handleMintPersonalKey — the one
// state-changing action beyond auth itself that reads the session cookie
// to authorize something with real consequences, a self-service API key):
// all three are covered by the session cookie's SameSite=Lax attribute,
// which browsers don't attach to a cross-site POST, so a third-party page
// cannot forge any of them. Revisit this reasoning if a future
// session-authenticated route needs to be reachable via a mechanism
// SameSite=Lax doesn't cover (e.g. a simple cross-site GET/form submission
// treated as same-site, or a route that must also work over WebSocket/SSE
// where SameSite enforcement differs) — don't assume SameSite=Lax alone
// covers every future case without rechecking against how that route is
// actually reached.
type sessionContextKey struct{}

// requireSession wraps a UI handler (or sub-mux of handlers) so every
// request must carry a valid session cookie. Missing or invalid sessions
// redirect to /login?next=<original path> rather than returning a bare
// 401: unlike the REST API's authenticate middleware, this guards a
// browser-navigated surface, so a redirect is the right UX.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			redirectToLogin(w, r)
			return
		}
		principal, err := auth.AuthenticateSession(r.Context(), s.store, cookie.Value, time.Now())
		if err != nil {
			redirectToLogin(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, principal)))
	})
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := url.QueryEscape(r.URL.RequestURI())
	http.Redirect(w, r, "/login?next="+next, http.StatusFound)
}

func sessionPrincipalFromRequest(r *http.Request) (auth.SessionPrincipal, bool) {
	principal, ok := r.Context().Value(sessionContextKey{}).(auth.SessionPrincipal)
	return principal, ok
}

var loginPageTmpl = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/login.html"))

type loginPageData struct {
	Next        string
	Error       string
	OIDCEnabled bool
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if _, err := auth.AuthenticateSession(r.Context(), s.store, cookie.Value, time.Now()); err == nil {
			http.Redirect(w, r, safeNextPath(r.URL.Query().Get("next")), http.StatusFound) //nolint:gosec // safeNextPath only ever returns "/" or a same-origin relative path, never an absolute/external URL
			return
		}
	}
	data := loginPageData{Next: safeNextPath(r.URL.Query().Get("next")), OIDCEnabled: s.oidc != nil}
	if r.URL.Query().Get("error") == "1" {
		data.Error = "Invalid username or password, or single sign-on login failed."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := loginPageTmpl.ExecuteTemplate(w, "login.html", data); err != nil {
		slog.Error("login page render", "err", err)
	}
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	next := safeNextPath(r.FormValue("next"))

	account, err := s.store.GetLocalAdmin(r.Context())
	if err != nil || username != account.Username || !auth.VerifyPassword(account.PasswordHash, password) {
		// Deliberately the same redirect for "no account bootstrapped yet",
		// "unknown username", and "wrong password" — an attacker should not
		// be able to distinguish these from the response.
		http.Redirect(w, r, "/login?error=1&next="+url.QueryEscape(next), http.StatusFound)
		return
	}

	if err := s.mintSession(w, r, model.SessionKindLocalAdmin, account.Username, model.APIKeyRoleAdmin); err != nil {
		slog.Error("mint session", "err", err)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	http.Redirect(w, r, next, http.StatusFound) //nolint:gosec // next was assigned from safeNextPath above, never an absolute/external URL
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if principal, err := auth.AuthenticateSession(r.Context(), s.store, cookie.Value, time.Now()); err == nil {
			if err := s.store.DeleteSession(r.Context(), principal.SessionID); err != nil && !errors.Is(err, store.ErrNotFound) {
				slog.Error("delete session on logout", "err", err)
			}
		}
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // HttpOnly/Secure/SameSite are all set below; gosec's static check can't see through the !s.insecureHTTP expression (Secure is conditional, not a literal, so --insecure local dev over plain HTTP still works)
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   !s.insecureHTTP,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// SessionSweeper deletes expired sessions from the store. Without this,
// state.json would grow every session record ever minted for the life of
// the daemon, and AuthenticateSession's ListSessions-plus-linear-scan (the
// same pattern as API-key auth) would keep scanning stale entries on every
// UI request forever. Wired into the daemon's existing 10s reconcile tick
// (internal/cli/serve.go's serveLoop) alongside the sandbox deletion
// reconciler, rather than as a new standalone ticker.
type SessionSweeper struct {
	store store.Store
}

// NewSessionSweeper returns a SessionSweeper backed by st.
func NewSessionSweeper(st store.Store) *SessionSweeper {
	return &SessionSweeper{store: st}
}

// Reconcile deletes every session past its ExpiresAt. Matches the
// Reconcile(ctx) error shape internal/cli/serve.go's serveSandboxReconciler
// interface already expects, so it plugs into the existing reconcile pass
// with no new interface.
func (sw *SessionSweeper) Reconcile(ctx context.Context) error {
	sessions, err := sw.store.ListSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	now := time.Now()
	var firstErr error
	for _, session := range sessions {
		if !session.Expired(now) {
			continue
		}
		if err := sw.store.DeleteSession(ctx, session.ID); err != nil && !errors.Is(err, store.ErrNotFound) && firstErr == nil {
			firstErr = fmt.Errorf("delete expired session %q: %w", session.ID, err)
		}
	}
	return firstErr
}

// safeNextPath only allows redirecting back to a same-origin, relative path
// (never an absolute URL) so a crafted ?next= query value can't be used for
// an open-redirect phishing hop off the Boxy dashboard. Rejects a leading
// "//" or "/\" specifically: browsers normalize a backslash in the path
// position of a Location header to a forward slash, so "/\evil.com" is
// exactly as much a protocol-relative redirect to an external host as
// "//evil.com" is — checking only for "//" would miss it.
func safeNextPath(next string) string {
	if next == "" || next[0] != '/' {
		return "/"
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return "/"
	}
	return next
}
