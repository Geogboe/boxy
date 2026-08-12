package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Geogboe/boxy/internal/auth"
	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
)

type principalContextKey struct{}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/api-keys/bootstrap" {
			next.ServeHTTP(w, r)
			return
		}

		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) || strings.TrimSpace(strings.TrimPrefix(header, prefix)) == "" {
			httpjson.Error(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		principal, err := auth.Authenticate(r.Context(), s.store, strings.TrimSpace(strings.TrimPrefix(header, prefix)), time.Now())
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				httpjson.Error(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}
			httpjson.Error(w, http.StatusInternalServerError, "failed to authenticate request")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}

func principalFromRequest(r *http.Request) auth.Principal {
	if principal, ok := r.Context().Value(principalContextKey{}).(auth.Principal); ok {
		return principal
	}
	// Unauthenticated in-process servers are used by the existing test mux and
	// are treated as an admin-equivalent local caller.
	return auth.Principal{Role: model.APIKeyRoleAdmin}
}

func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, roles ...model.APIKeyRole) bool {
	role := principalFromRequest(r).Role
	for _, allowed := range roles {
		if role == allowed {
			return true
		}
	}
	httpjson.Error(w, http.StatusForbidden, "insufficient role for this operation")
	return false
}

func (s *Server) authorizeSandbox(w http.ResponseWriter, r *http.Request, sb model.Sandbox, mutate bool) bool {
	principal := principalFromRequest(r)
	switch principal.Role {
	case model.APIKeyRoleAdmin:
		return true
	case model.APIKeyRoleAuditor:
		if mutate {
			httpjson.Error(w, http.StatusForbidden, "auditor keys cannot mutate sandboxes")
			return false
		}
		return true
	case model.APIKeyRoleUser:
		if !s.authRequired || sb.OwnerID == string(principal.KeyID) {
			return true
		}
		httpjson.Error(w, http.StatusForbidden, "sandbox belongs to another API-key owner")
		return false
	default:
		httpjson.Error(w, http.StatusForbidden, "insufficient role for this operation")
		return false
	}
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
