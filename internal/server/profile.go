package server

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
)

// defaultPersonalKeyMaxTTL bounds a personal key self-service-minted from
// the web UI profile page when no server.oidc.personal_key_max_ttl applies
// -- notably the bootstrapped local-admin session, which has no OIDC
// config to read a TTL from at all. Matches sessionTTL: a personal key
// minted from a 12h session shouldn't outlive the session that minted it
// by an arbitrary amount.
const defaultPersonalKeyMaxTTL = 12 * time.Hour

// profileData is the profile page's pageData.Profile payload.
type profileData struct {
	Subject      string
	Role         model.APIKeyRole
	SignInMethod string
	MintedKey    string
	MintedName   string
	MintError    string
	PersonalKeys []apiKeySummary
}

// signInMethodLabel renders a session Kind for display -- deliberately not
// just the raw model.SessionKind string ("local_admin", "oidc"), which
// reads like an internal enum rather than UI copy.
func signInMethodLabel(kind model.SessionKind) string {
	switch kind {
	case model.SessionKindOIDC:
		return "Single sign-on"
	case model.SessionKindLocalAdmin:
		return "Local admin account"
	default:
		return string(kind)
	}
}

// personalKeySubject returns the stable OwnerID-bearing identity (see
// Decision 5 of docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md)
// a session's principal mints self-service personal keys under. OIDC
// sessions use the "oidc:<subject>" shape the CLI's device-code exchange
// already uses; the bootstrapped local-admin account has no OIDC subject to
// borrow, so it gets the analogous "local:<username>" shape instead --
// still stable across the account's short-lived sessions and personal
// keys, just scoped to a different namespace.
func personalKeySubject(kind model.SessionKind, subject string) string {
	if kind == model.SessionKindOIDC {
		return "oidc:" + subject
	}
	return "local:" + subject
}

func (s *Server) profileData(r *http.Request) (pageData, error) {
	principal, ok := sessionPrincipalFromRequest(r)
	if !ok {
		return pageData{}, nil
	}
	keys, err := s.personalAPIKeySummaries(r.Context(), personalKeySubject(principal.Kind, principal.Subject))
	if err != nil {
		return pageData{}, err
	}
	return pageData{Profile: profileData{
		Subject:      principal.Subject,
		Role:         principal.Role,
		SignInMethod: signInMethodLabel(principal.Kind),
		PersonalKeys: keys,
	}}, nil
}

func (s *Server) personalAPIKeySummaries(ctx context.Context, subject string) ([]apiKeySummary, error) {
	keys, err := s.store.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apiKeySummary, 0, len(keys))
	for _, key := range keys {
		if key.EffectiveKind() != model.APIKeyKindPersonal || key.Subject != subject {
			continue
		}
		out = append(out, apiKeySummary{
			ID: key.ID, Name: key.Name, Role: key.Role, CreatedAt: key.CreatedAt,
			ExpiresAt: key.ExpiresAt, RevokedAt: key.RevokedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// handleMintPersonalKey mints a self-service personal API key for the
// currently logged-in session and renders it once directly in the response
// page -- never via a redirect, which would otherwise put the raw key in a
// URL that lands in browser history and server access logs. Protected the
// same way POST /login and POST /logout are: no separate CSRF token,
// relying on the session cookie's SameSite=Lax (see session.go's comment on
// sessionContextKey) -- this is a same-origin form POST reachable only by a
// page that already has the session cookie, so a cross-site forged POST
// arrives with no cookie attached at all.
func (s *Server) handleMintPersonalKey(profileTmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := sessionPrincipalFromRequest(r)
		if !ok {
			redirectToLogin(w, r)
			return
		}

		d := pageData{
			Nav:                  "profile",
			User:                 principal.Subject,
			CanManageServiceKeys: principal.Role == model.APIKeyRoleAdmin,
			Profile: profileData{
				Subject:      principal.Subject,
				Role:         principal.Role,
				SignInMethod: signInMethodLabel(principal.Kind),
			},
		}
		d.Profile.PersonalKeys, _ = s.personalAPIKeySummaries(r.Context(), personalKeySubject(principal.Kind, principal.Subject))

		if err := r.ParseForm(); err != nil {
			d.Profile.MintError = "Invalid request."
		} else {
			name := strings.TrimSpace(r.FormValue("name"))
			if name == "" {
				name = "web-profile"
			}
			ttl := defaultPersonalKeyMaxTTL
			if s.oidc != nil && principal.Kind == model.SessionKindOIDC && s.oidc.PersonalKeyMaxTTL > 0 {
				ttl = s.oidc.PersonalKeyMaxTTL
			}
			subject := personalKeySubject(principal.Kind, principal.Subject)
			resp, err := s.mintPersonalAPIKey(r.Context(), subject, principal.Role, name, ttl)
			if err != nil {
				slog.Error("mint personal key", "err", err)
				d.Profile.MintError = "Failed to generate a key. Please try again."
			} else {
				d.Profile.MintedKey = resp.Key
				d.Profile.MintedName = resp.Name
				d.Profile.PersonalKeys, _ = s.personalAPIKeySummaries(r.Context(), subject)
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// This response can carry a freshly minted raw API key -- never
		// let it be cached by the browser or an intermediary.
		w.Header().Set("Cache-Control", "no-store")
		if err := profileTmpl.ExecuteTemplate(w, "layout.html", d); err != nil {
			slog.Error("profile page render", "err", err)
		}
	}
}
