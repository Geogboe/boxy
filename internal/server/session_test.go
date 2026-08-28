package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func seedLocalAdmin(t *testing.T, st store.Store, password string) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := st.PutLocalAdmin(t.Context(), model.LocalAdminAccount{
		Username:     model.LocalAdminUsername,
		PasswordHash: hash,
	}); err != nil {
		t.Fatalf("PutLocalAdmin: %v", err)
	}
}

func TestUI_unauthenticatedRequestRedirectsToLogin(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/pools", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("Location = %q, want a /login?next= redirect", loc)
	}
}

func TestUI_loginPageRendersUnauthenticated(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `name="password"`) {
		t.Fatalf("login page missing password field, body = %q", w.Body.String())
	}
}

func TestUI_loginWithCorrectPasswordSetsSessionCookieAndRedirects(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	seedLocalAdmin(t, st, "correct horse battery staple")
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	form := url.Values{"username": {model.LocalAdminUsername}, "password": {"correct horse battery staple"}, "next": {"/ui/pools"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/ui/pools" {
		t.Fatalf("Location = %q, want /ui/pools", loc)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "boxy_session" || cookies[0].Value == "" {
		t.Fatalf("cookies = %+v, want exactly one non-empty boxy_session cookie", cookies)
	}
	if !cookies[0].HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}

	// The minted cookie must actually authenticate a follow-up request.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/ui/pools", nil)
	r2.AddCookie(cookies[0])
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("authenticated request status = %d, want %d", w2.Code, http.StatusOK)
	}
}

func TestUI_loginWithWrongPasswordRedirectsBackWithError(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	seedLocalAdmin(t, st, "correct horse battery staple")
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	form := url.Values{"username": {model.LocalAdminUsername}, "password": {"wrong password"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login?error=1") {
		t.Fatalf("Location = %q, want a /login?error=1 redirect", loc)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("cookies = %+v, want none set on failed login", w.Result().Cookies())
	}
}

func TestUI_loginWithNoBootstrappedAdminRedirectsBackWithError(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore() // no PutLocalAdmin call — nothing bootstrapped
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	form := url.Values{"username": {model.LocalAdminUsername}, "password": {"anything"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login?error=1") {
		t.Fatalf("Location = %q, want a /login?error=1 redirect", loc)
	}
}

func TestUI_logoutClearsCookieAndInvalidatesSession(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/logout", nil)
	r.AddCookie(&http.Cookie{Name: "boxy_session", Value: server.TestSessionCookieValue})
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("cookies = %+v, want a single cleared (MaxAge<0) boxy_session cookie", cookies)
	}

	// The now-deleted session must no longer authenticate.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/ui/pools", nil)
	r2.AddCookie(&http.Cookie{Name: "boxy_session", Value: server.TestSessionCookieValue})
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusFound {
		t.Fatalf("post-logout request status = %d, want %d (redirect to login)", w2.Code, http.StatusFound)
	}
}

// TestUI_loginRejectsOpenRedirectNextValues guards safeNextPath against
// protocol-relative and backslash-based redirect targets: "//evil.com" and
// "/\evil.com" both look like a same-origin path (start with '/') but a
// browser treats either as a scheme-relative redirect to an external host
// once placed in a Location header. Both must fall back to "/".
func TestUI_loginRejectsOpenRedirectNextValues(t *testing.T) {
	t.Parallel()
	for _, next := range []string{"//evil.example.invalid", `/\evil.example.invalid`, "https://evil.example.invalid", "not-a-path"} {
		st := store.NewMemoryStore()
		seedLocalAdmin(t, st, "correct horse battery staple")
		mux := server.NewTestMux(st, sandbox.New(st, nil), true)

		form := url.Values{"username": {model.LocalAdminUsername}, "password": {"correct horse battery staple"}, "next": {next}}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(w, r)

		if loc := w.Header().Get("Location"); loc != "/" {
			t.Errorf("next = %q: Location = %q, want / (open-redirect guard should have rejected it)", next, loc)
		}
	}
}

func TestSessionSweeper_DeletesOnlyExpiredSessions(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	ctx := context.Background()
	now := time.Now()

	expired := model.Session{ID: "expired", Hash: "h1", Role: model.APIKeyRoleAdmin, ExpiresAt: now.Add(-time.Hour)}
	stillValid := model.Session{ID: "valid", Hash: "h2", Role: model.APIKeyRoleAdmin, ExpiresAt: now.Add(time.Hour)}
	if err := st.PutSession(ctx, expired); err != nil {
		t.Fatalf("PutSession(expired): %v", err)
	}
	if err := st.PutSession(ctx, stillValid); err != nil {
		t.Fatalf("PutSession(valid): %v", err)
	}

	sweeper := server.NewSessionSweeper(st)
	if err := sweeper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if _, err := st.GetSession(ctx, expired.ID); err != store.ErrNotFound {
		t.Fatalf("GetSession(expired) after sweep = %v, want ErrNotFound", err)
	}
	if _, err := st.GetSession(ctx, stillValid.ID); err != nil {
		t.Fatalf("GetSession(valid) after sweep: %v, want it to survive", err)
	}
}

func TestUI_alreadyLoggedInVisitingLoginRedirectsHome(t *testing.T) {
	t.Parallel()
	st := store.NewMemoryStore()
	mux := server.NewTestMux(st, sandbox.New(st, nil), true)

	w := httptest.NewRecorder()
	r := server.AuthedRequest(httptest.NewRequest(http.MethodGet, "/login", nil))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want /", loc)
	}
}
