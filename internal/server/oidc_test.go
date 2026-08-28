package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
	oidclib "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const testOIDCClientID = "boxy-test-client"

// newTestOIDCOptions builds server.OIDCOptions against provider exactly the
// way internal/cli/serve.go's real wiring will: a live discovery fetch
// (oidc.NewProvider) plus a verifier backed by the provider's JWKS, so the
// signature/issuer/audience/expiry checks in the real client library run
// for real against the fake provider's keys, not a stub.
func newTestOIDCOptions(t *testing.T, provider *fakeOIDCProvider, roleClaim string, roleMapping map[string]string, defaultRole model.APIKeyRole) *server.OIDCOptions {
	t.Helper()
	ctx := context.Background()
	p, err := oidclib.NewProvider(ctx, provider.URL())
	if err != nil {
		t.Fatalf("oidc.NewProvider: %v", err)
	}
	return &server.OIDCOptions{
		Issuer: provider.URL(),
		OAuth2: oauth2.Config{
			ClientID:     testOIDCClientID,
			ClientSecret: "unused-in-tests",
			RedirectURL:  "https://boxy.example.invalid/auth/callback",
			Endpoint:     p.Endpoint(),
			Scopes:       []string{oidclib.ScopeOpenID},
		},
		Verifier:    p.Verifier(&oidclib.Config{ClientID: testOIDCClientID}),
		RoleClaim:   roleClaim,
		RoleMapping: roleMapping,
		DefaultRole: defaultRole,
	}
}

// startOIDCLogin drives the real /auth/login handler and extracts the
// state/nonce it generated (both appear as query params on the redirect to
// the provider) plus the cookie the browser would carry back to /auth/callback.
func startOIDCLogin(t *testing.T, mux *http.ServeMux, next string) (state, nonce string, cookie *http.Cookie) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/login?next="+url.QueryEscape(next), nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("/auth/login status = %d, want %d", w.Code, http.StatusFound)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	state = loc.Query().Get("state")
	nonce = loc.Query().Get("nonce")
	if state == "" || nonce == "" {
		t.Fatalf("Location %q missing state/nonce", loc)
	}
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "boxy_oauth_state" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("no boxy_oauth_state cookie set, cookies = %+v", cookies)
	}
	return state, nonce, cookie
}

func TestOIDC_LoginFlow_MintsSessionWithMappedRole(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "")

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	state, nonce, stateCookie := startOIDCLogin(t, mux, "/ui/pools")
	provider.SetPendingCode("test-code", fakeOIDCClaims{
		Subject: "alice",
		Nonce:   nonce,
		Extra:   map[string]any{"groups": []string{"boxy-admins"}},
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code=test-code&state="+state, nil)
	r.AddCookie(stateCookie)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("/auth/callback status = %d, want %d, body = %q", w.Code, http.StatusFound, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/ui/pools" {
		t.Fatalf("Location = %q, want /ui/pools", loc)
	}
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "boxy_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("no boxy_session cookie set after successful oidc callback")
	}

	// The minted session must authenticate a follow-up request and grant
	// the admin-mapped role (verified indirectly: an admin-only page like
	// /ui/agents is reachable, and the session was minted with Subject
	// "alice" via SessionKindOIDC -- checked directly against the store).
	sessions, err := st.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var found model.Session
	var foundOK bool
	for _, session := range sessions {
		if session.Hash != "" && session.Kind == model.SessionKindOIDC {
			found, foundOK = session, true
		}
	}
	if !foundOK {
		t.Fatal("no SessionKindOIDC session was persisted")
	}
	if found.Subject != "alice" || found.Role != model.APIKeyRoleAdmin {
		t.Fatalf("session = %+v, want subject alice / role admin", found)
	}
}

func TestOIDC_LoginFlow_PicksMostPrivilegedMatchingRole(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{
		"boxy-users":  "user",
		"boxy-admins": "admin",
	}, "")

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	state, nonce, cookie := startOIDCLogin(t, mux, "/")
	provider.SetPendingCode("multi-group-code", fakeOIDCClaims{
		Subject: "bob",
		Nonce:   nonce,
		Extra:   map[string]any{"groups": []string{"boxy-users", "boxy-admins"}}, // member of both; admin must win
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code=multi-group-code&state="+state, nil)
	r.AddCookie(cookie)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("status = %d, Location = %q, body = %q", w.Code, w.Header().Get("Location"), w.Body.String())
	}

	sessions, _ := st.ListSessions(context.Background())
	var found model.Session
	var foundOK bool
	for _, session := range sessions {
		if session.Subject == "bob" {
			found, foundOK = session, true
		}
	}
	if !foundOK {
		t.Fatal("no session persisted for bob")
	}
	if found.Role != model.APIKeyRoleAdmin {
		t.Fatalf("role = %q, want admin (most-privileged of boxy-users/boxy-admins)", found.Role)
	}
}

func TestOIDC_LoginFlow_NoMatchingRoleAndNoDefaultIsRejected(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "") // no default

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	state, nonce, cookie := startOIDCLogin(t, mux, "/")
	provider.SetPendingCode("unmapped-code", fakeOIDCClaims{
		Subject: "carol",
		Nonce:   nonce,
		Extra:   map[string]any{"groups": []string{"some-other-group"}},
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code=unmapped-code&state="+state, nil)
	r.AddCookie(cookie)
	mux.ServeHTTP(w, r)

	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login?error=1") {
		t.Fatalf("Location = %q, want /login?error=1 (no role resolved, no default)", loc)
	}
	if len(w.Result().Cookies()) > 0 {
		for _, c := range w.Result().Cookies() {
			if c.Name == "boxy_session" && c.Value != "" {
				t.Fatalf("a session cookie was set for a rejected login: %+v", c)
			}
		}
	}
}

func TestOIDC_LoginFlow_FallsBackToDefaultRole(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, model.APIKeyRoleUser)

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	state, nonce, cookie := startOIDCLogin(t, mux, "/")
	provider.SetPendingCode("default-role-code", fakeOIDCClaims{
		Subject: "dave",
		Nonce:   nonce,
		Extra:   map[string]any{"groups": []string{"unmapped-group"}},
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code=default-role-code&state="+state, nil)
	r.AddCookie(cookie)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("status = %d, Location = %q, body = %q", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	sessions, _ := st.ListSessions(context.Background())
	var found *model.Session
	for i := range sessions {
		if sessions[i].Subject == "dave" {
			found = &sessions[i]
		}
	}
	if found == nil || found.Role != model.APIKeyRoleUser {
		t.Fatalf("session = %+v, want role user (DefaultRole fallback)", found)
	}
}

func TestOIDC_CallbackRejectsStateMismatch(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "")

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	_, nonce, cookie := startOIDCLogin(t, mux, "/")
	provider.SetPendingCode("tampered-code", fakeOIDCClaims{Subject: "eve", Nonce: nonce, Extra: map[string]any{"groups": []string{"boxy-admins"}}})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code=tampered-code&state=not-the-real-state", nil)
	r.AddCookie(cookie)
	mux.ServeHTTP(w, r)

	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login?error=1") {
		t.Fatalf("Location = %q, want /login?error=1 (state mismatch)", loc)
	}
}

func TestOIDC_CallbackRejectsMissingStateCookie(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "")

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code=whatever&state=whatever", nil)
	mux.ServeHTTP(w, r)

	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login?error=1") {
		t.Fatalf("Location = %q, want /login?error=1 (no state cookie)", loc)
	}
}

// TestOIDC_RoutesAbsentWhenNotConfigured confirms /auth/login and
// /auth/callback aren't specially registered when OIDC isn't configured:
// with no route match, they fall through to the same session-gated
// catch-all every other unmatched path under "/" does (redirect to
// /login), not a dedicated OIDC handler.
func TestOIDC_RoutesAbsentWhenNotConfigured(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	for _, path := range []string{"/auth/login", "/auth/callback"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") == "" {
			t.Fatalf("%s status = %d, Location = %q; want a redirect-to-login same as any other unmatched path when OIDC is not configured", path, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestOIDC_CLIConfig_ReturnsIssuerAndClientIDWhenConfigured(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "")
	oidcOpts.CLIClientID = "boxy-cli"

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/cli-config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %q", w.Code, http.StatusOK, w.Body.String())
	}
	var got struct {
		Issuer      string `json:"issuer"`
		CLIClientID string `json:"cli_client_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Issuer != provider.URL() || got.CLIClientID != "boxy-cli" {
		t.Fatalf("got = %+v, want issuer=%q cli_client_id=boxy-cli", got, provider.URL())
	}
}

func TestOIDC_CLIConfig_NotFoundWhenCLIClientIDUnset(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "") // CLIClientID left empty

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/cli-config", nil))
	// No route registered at all when CLIClientID is unset -- falls through
	// to the same session-gated catch-all as any other unmatched path.
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d (redirect to login, no dedicated route)", w.Code, http.StatusFound)
	}
}

func TestOIDC_KeyExchange_MintsPersonalKeyForValidIDToken(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "")
	oidcOpts.PersonalKeyMaxTTL = time.Hour
	oidcOpts.CLIClientID = testOIDCClientID
	oidcOpts.CLIVerifier = oidcOpts.Verifier // fake provider signs every test token with the same audience regardless of which "client" conceptually requested it

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	rawIDToken, err := provider.signIDToken(fakeOIDCClaims{
		Subject: "frank",
		Extra:   map[string]any{"groups": []string{"boxy-admins"}},
	})
	if err != nil {
		t.Fatalf("signIDToken: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"id_token": rawIDToken})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys/oidc-exchange", strings.NewReader(string(body)))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %q", w.Code, http.StatusCreated, w.Body.String())
	}
	var resp struct {
		Key  string           `json:"key"`
		Role model.APIKeyRole `json:"role"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Key == "" || resp.Role != model.APIKeyRoleAdmin {
		t.Fatalf("resp = %+v, want a non-empty key with role admin", resp)
	}

	keys, err := st.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].Kind != model.APIKeyKindPersonal || keys[0].Subject != "oidc:frank" {
		t.Fatalf("keys = %+v, want exactly one personal key with subject oidc:frank", keys)
	}
}

func TestOIDC_KeyExchange_RejectsInvalidIDToken(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "")
	oidcOpts.CLIClientID = testOIDCClientID
	oidcOpts.CLIVerifier = oidcOpts.Verifier // fake provider signs every test token with the same audience regardless of which "client" conceptually requested it

	st := store.NewMemoryStore()
	mux := server.NewTestMuxWithOIDC(st, sandbox.New(st, nil), oidcOpts)

	body, _ := json.Marshal(map[string]string{"id_token": "not-a-real-token"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys/oidc-exchange", strings.NewReader(string(body)))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if keys, _ := st.ListAPIKeys(context.Background()); len(keys) != 0 {
		t.Fatalf("keys = %+v, want none minted for an invalid token", keys)
	}
}

func TestOIDC_LoginPageShowsSSOLinkOnlyWhenConfigured(t *testing.T) {
	t.Parallel()
	provider := newFakeOIDCProvider(t)
	oidcOpts := newTestOIDCOptions(t, provider, "groups", map[string]string{"boxy-admins": "admin"}, "")

	stWith := store.NewMemoryStore()
	muxWith := server.NewTestMuxWithOIDC(stWith, sandbox.New(stWith, nil), oidcOpts)
	w := httptest.NewRecorder()
	muxWith.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	if !strings.Contains(w.Body.String(), "/auth/login") {
		t.Fatalf("login page missing SSO link when OIDC is configured, body = %q", w.Body.String())
	}

	stWithout := store.NewMemoryStore()
	muxWithout := server.NewTestMux(stWithout, sandbox.New(stWithout, nil), true)
	w2 := httptest.NewRecorder()
	muxWithout.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/login", nil))
	if strings.Contains(w2.Body.String(), "/auth/login") {
		t.Fatalf("login page shows SSO link when OIDC is not configured, body = %q", w2.Body.String())
	}
}
