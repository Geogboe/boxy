package server

import (
	"net/http"

	"github.com/Geogboe/boxy/pkg/httpjson"
	"github.com/Geogboe/boxy/pkg/model"
)

// identityResponse is the role-neutral "who am I" view of the authenticated
// principal. It carries only what identifies the caller to itself -- never
// the key hash or any other credential material -- so it is safe for any
// authenticated role, including `user`, to read.
type identityResponse struct {
	KeyID   model.APIKeyID   `json:"key_id"`
	Role    model.APIKeyRole `json:"role"`
	Kind    model.APIKeyKind `json:"kind,omitempty"`
	Subject string           `json:"subject,omitempty"`
}

// handleIdentity returns the authenticated caller's own principal. It is the
// role-neutral endpoint `boxy login` verifies a supplied key against: unlike
// /api/v1/pools, every valid role -- including `user` -- can call it, so
// login verification no longer depends on a privileged endpoint (#359). It
// performs no role check beyond the authenticate middleware itself already
// requiring a valid bearer credential.
func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	// Every valid role is allowed here -- this call is a no-op against the
	// authenticate middleware today, kept for the same fail-closed reason
	// every other handler in this package opens with requireRole: if a
	// fourth role is ever added without updating this list, a key with
	// that role gets 403 here instead of silently succeeding.
	if !s.requireRole(w, r, model.APIKeyRoleUser, model.APIKeyRoleAuditor, model.APIKeyRoleAdmin) {
		return
	}
	principal := principalFromRequest(r)
	httpjson.Write(w, http.StatusOK, identityResponse{
		KeyID:   principal.KeyID,
		Role:    principal.Role,
		Kind:    principal.Kind,
		Subject: principal.Subject,
	})
}
