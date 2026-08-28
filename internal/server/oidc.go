package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// OIDCOptions configures browser login against an external OpenID Connect
// provider. Built by the caller (internal/cli/serve.go, from
// config.ServerSpec.OIDC) since constructing OAuth2/Verifier requires a
// live discovery fetch against the issuer -- this package stays free of
// any config-file decoding concern, the same separation ServerOptions
// already keeps for TLS material and guest secrets.
type OIDCOptions struct {
	// Issuer is the provider's issuer URL, exposed to the CLI (via
	// GET /auth/cli-config) so `boxy login --oidc` can run its own
	// discovery -- the CLI talks to the provider directly for the
	// device-code grant, it does not proxy through this server.
	Issuer string
	OAuth2 oauth2.Config
	// Verifier checks the audience against the confidential web client
	// (OAuth2.ClientID) -- used only by the browser callback.
	Verifier *oidc.IDTokenVerifier
	// RoleClaim names the ID token claim whose value(s) are looked up in
	// RoleMapping. May be a single string or an array of strings.
	RoleClaim string
	// RoleMapping maps a RoleClaim value to a Boxy role.
	RoleMapping map[string]string
	// DefaultRole is used when no RoleClaim value matches RoleMapping.
	// Empty fails closed (login rejected).
	DefaultRole model.APIKeyRole
	// CLIClientID, if set, is the public (no-secret) OAuth2 client ID
	// `boxy login --oidc` uses for the device-code grant. Empty means
	// CLI OIDC login is unavailable (GET /auth/cli-config 404s).
	CLIClientID string
	// CLIVerifier checks the audience against CLIClientID rather than the
	// web client -- an ID token minted for the CLI's own device-flow
	// client carries that audience, not the web client's, so reusing
	// Verifier here would always fail with an audience mismatch. Set
	// only when CLIClientID is.
	CLIVerifier *oidc.IDTokenVerifier
	// PersonalKeyMaxTTL bounds how long a self-service personal API key
	// minted via POST /api/v1/api-keys/oidc-exchange may live.
	PersonalKeyMaxTTL time.Duration
}

// oauthStateCookieName carries the CSRF state, replay-protection nonce, and
// post-login redirect target across the round trip to the IdP and back.
// Scoped to /auth (Path) since it's only ever read by the two routes below.
const oauthStateCookieName = "boxy_oauth_state"

type oauthState struct {
	State string `json:"state"`
	Nonce string `json:"nonce"`
	Next  string `json:"next"`
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// handleOIDCLogin starts the OIDC authorization-code flow: mint state and a
// replay-protection nonce, stash them (plus the sanitized post-login
// redirect target) in a short-lived cookie, and redirect to the provider.
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	state, err := randomToken()
	if err != nil {
		slog.Error("generate oidc state", "err", err)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		slog.Error("generate oidc nonce", "err", err)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	st := oauthState{State: state, Nonce: nonce, Next: safeNextPath(r.URL.Query().Get("next"))}
	raw, err := json.Marshal(st)
	if err != nil {
		slog.Error("marshal oidc state", "err", err)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // HttpOnly/Secure/SameSite are all set; Secure is conditional on !s.insecureHTTP so --insecure local dev over plain HTTP still works
		Name:     oauthStateCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/auth",
		HttpOnly: true,
		Secure:   !s.insecureHTTP,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	http.Redirect(w, r, s.oidc.OAuth2.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}

// handleOIDCCallback completes the flow: validate state (CSRF), exchange
// the code, verify the ID token's signature/issuer/audience/expiry/nonce,
// resolve a role from its claims, and mint the same kind of server-side
// session a local-admin login does.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	var next string
	fail := func(reason string, err error) {
		if err != nil {
			slog.Error("oidc callback", "reason", reason, "err", err)
		} else {
			slog.Warn("oidc callback", "reason", reason)
		}
		clearOAuthStateCookie(w, s.insecureHTTP)
		http.Redirect(w, r, "/login?error=1", http.StatusFound)
	}

	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil || cookie.Value == "" {
		fail("missing state cookie", nil)
		return
	}
	rawState, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		fail("decode state cookie", err)
		return
	}
	var st oauthState
	if err := json.Unmarshal(rawState, &st); err != nil {
		fail("unmarshal state cookie", err)
		return
	}
	next = st.Next
	clearOAuthStateCookie(w, s.insecureHTTP)

	if st.State == "" || r.URL.Query().Get("state") != st.State {
		fail("state mismatch", nil)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail("missing code", nil)
		return
	}

	token, err := s.oidc.OAuth2.Exchange(r.Context(), code)
	if err != nil {
		fail("token exchange", err)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		fail("no id_token in token response", nil)
		return
	}
	idToken, err := s.oidc.Verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		fail("verify id_token", err)
		return
	}
	if idToken.Nonce != st.Nonce {
		fail("nonce mismatch", nil)
		return
	}
	if idToken.Subject == "" {
		fail("id_token has no subject", nil)
		return
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		fail("decode claims", err)
		return
	}
	role := resolveOIDCRole(s.oidc, claims)
	if role == "" {
		fail("no role resolved from claims", nil)
		return
	}

	if err := s.mintSession(w, r, model.SessionKindOIDC, idToken.Subject, role); err != nil {
		fail("mint session", err)
		return
	}
	http.Redirect(w, r, next, http.StatusFound) //nolint:gosec // next comes from oauthState.Next, itself only ever set via safeNextPath in handleOIDCLogin
}

// resolveOIDCRole looks up every value of opts.RoleClaim (a single string
// or an array of strings) in opts.RoleMapping and returns the
// most-privileged matching role (admin > auditor > user), so a principal
// in multiple mapped groups isn't order-dependent on the provider's own
// claim ordering. Returns opts.DefaultRole (possibly "") when nothing
// matches.
func resolveOIDCRole(opts *OIDCOptions, claims map[string]any) model.APIKeyRole {
	best := opts.DefaultRole
	consider := func(value string) {
		mapped, ok := opts.RoleMapping[value]
		if !ok {
			return
		}
		role := model.APIKeyRole(mapped)
		if rolePrecedence(role) > rolePrecedence(best) {
			best = role
		}
	}

	switch v := claims[opts.RoleClaim].(type) {
	case string:
		consider(v)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				consider(s)
			}
		}
	}
	return best
}

func rolePrecedence(role model.APIKeyRole) int {
	switch role {
	case model.APIKeyRoleUser:
		return 1
	case model.APIKeyRoleAuditor:
		return 2
	case model.APIKeyRoleAdmin:
		return 3
	default:
		return 0
	}
}

func clearOAuthStateCookie(w http.ResponseWriter, insecureHTTP bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // HttpOnly/Secure/SameSite are all set; Secure is conditional on !insecureHTTP so --insecure local dev over plain HTTP still works
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/auth",
		HttpOnly: true,
		Secure:   !insecureHTTP,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// mintSession persists a new model.Session and sets its cookie. Shared by
// the local-admin (handleLoginSubmit) and OIDC (handleOIDCCallback) login
// paths -- only how the principal was established differs between them.
func (s *Server) mintSession(w http.ResponseWriter, r *http.Request, kind model.SessionKind, subject string, role model.APIKeyRole) error {
	raw, hash, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}
	now := time.Now()
	session := model.Session{
		ID:        model.SessionID(uuid.NewString()),
		Hash:      hash,
		Kind:      kind,
		Subject:   subject,
		Role:      role,
		CreatedAt: now,
		ExpiresAt: now.Add(s.effectiveSessionTTL()),
	}
	if err := s.store.PutSession(r.Context(), session); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // HttpOnly/Secure/SameSite are all set; Secure is conditional on !s.insecureHTTP so --insecure local dev over plain HTTP still works
		Name:     sessionCookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   !s.insecureHTTP,
		SameSite: http.SameSiteLaxMode,
		Expires:  session.ExpiresAt,
	})
	return nil
}
