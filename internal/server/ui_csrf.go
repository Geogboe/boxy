package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

const csrfCookieName = "boxy_csrf"

// ensureCSRFCookie implements a double-submit token for state-changing UI
// forms. The token is readable by the page so it can be placed in a hidden
// field, but it is not HttpOnly; SameSite=Lax prevents a cross-site form from
// carrying the cookie and an attacker cannot read it to forge the field.
func ensureCSRFCookie(w http.ResponseWriter, r *http.Request, insecureHTTP bool) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is disabled only for explicit insecure local HTTP; SameSite=Lax limits cross-site form submission.
		Name: csrfCookieName, Value: token, Path: "/", Secure: !insecureHTTP,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

func validCSRFRequest(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	if err := r.ParseForm(); err != nil {
		return false
	}
	field := strings.TrimSpace(r.FormValue("csrf_token"))
	if field == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(field)) == 1
}

func requireUICSRF(w http.ResponseWriter, r *http.Request) bool {
	if validCSRFRequest(r) {
		return true
	}
	http.Error(w, "invalid CSRF token", http.StatusForbidden)
	return false
}
