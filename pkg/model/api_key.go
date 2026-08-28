package model

import "time"

// APIKeyID identifies an operator API key record.
type APIKeyID string

// APIKeyRole controls the operations available to an authenticated API key.
type APIKeyRole string

const (
	APIKeyRoleUser    APIKeyRole = "user"
	APIKeyRoleAuditor APIKeyRole = "auditor"
	APIKeyRoleAdmin   APIKeyRole = "admin"
)

// Valid reports whether the role is one of Boxy's supported API-key roles.
func (r APIKeyRole) Valid() bool {
	switch r {
	case APIKeyRoleUser, APIKeyRoleAuditor, APIKeyRoleAdmin:
		return true
	default:
		return false
	}
}

// APIKeyKind distinguishes an admin-issued, long-lived service credential
// from a self-service, short-lived personal one minted via OIDC login. An
// empty Kind on an existing persisted record means "service" -- see
// APIKey.EffectiveKind.
type APIKeyKind string

const (
	APIKeyKindService  APIKeyKind = "service"
	APIKeyKindPersonal APIKeyKind = "personal"
)

// APIKey is the persisted metadata for an operator credential. The raw key is
// deliberately never part of this model; Hash is the only credential material
// stored by the daemon.
type APIKey struct {
	ID        APIKeyID   `json:"id" yaml:"id"`
	Hash      string     `json:"hash" yaml:"hash"`
	Role      APIKeyRole `json:"role" yaml:"role"`
	Name      string     `json:"name,omitempty" yaml:"name,omitempty"`
	CreatedAt time.Time  `json:"created_at" yaml:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty" yaml:"revoked_at,omitempty"`
	// Kind distinguishes an admin-issued service key from a self-service
	// personal key. Empty on every key created before this field existed
	// and on every key the existing admin-only POST /api/v1/api-keys
	// endpoint creates -- see EffectiveKind.
	Kind APIKeyKind `json:"kind,omitempty" yaml:"kind,omitempty"`
	// Subject identifies the human a personal key was minted for (an
	// "oidc:<sub>" identity, see auth.SessionPrincipal). Empty for
	// service keys, which have no such identity to record.
	Subject string `json:"subject,omitempty" yaml:"subject,omitempty"`
}

// EffectiveKind returns k.Kind, defaulting to APIKeyKindService for a
// pre-existing record that predates this field.
func (k APIKey) EffectiveKind() APIKeyKind {
	if k.Kind == "" {
		return APIKeyKindService
	}
	return k.Kind
}

// Expired reports whether the key has an expiry in the past.
func (k APIKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)
}

// Revoked reports whether the key has been explicitly revoked.
func (k APIKey) Revoked() bool {
	return k.RevokedAt != nil
}
