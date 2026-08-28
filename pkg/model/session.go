package model

import "time"

// SessionID identifies a persisted web-UI login session record.
type SessionID string

// SessionKind identifies how a session's principal was established.
type SessionKind string

const (
	// SessionKindLocalAdmin is a session created by logging in with the
	// bootstrapped local admin account (see LocalAdminAccount).
	SessionKindLocalAdmin SessionKind = "local_admin"
	// SessionKindOIDC is a session created by a completed OIDC login.
	// Not produced anywhere yet; reserved for when OIDC support lands.
	SessionKindOIDC SessionKind = "oidc"
)

// Session is a server-side web-UI login session. This is a browser-only
// concept, separate from and unaffected by the CLI/API bearer-key model in
// APIKey: a session authenticates a human at the dashboard, not a
// programmatic REST caller. Only Hash is persisted; the raw cookie value is
// generated once at login (see internal/auth) and never stored, matching
// how APIKey stores a hash rather than the raw key.
type Session struct {
	ID   SessionID   `json:"id" yaml:"id"`
	Hash string      `json:"hash" yaml:"hash"`
	Kind SessionKind `json:"kind" yaml:"kind"`
	// Subject identifies the principal within Kind: the local admin
	// account's username for SessionKindLocalAdmin, or the OIDC subject
	// claim for SessionKindOIDC.
	Subject   string     `json:"subject" yaml:"subject"`
	Role      APIKeyRole `json:"role" yaml:"role"`
	CreatedAt time.Time  `json:"created_at" yaml:"created_at"`
	ExpiresAt time.Time  `json:"expires_at" yaml:"expires_at"`
}

// Expired reports whether the session's expiry has passed.
func (s Session) Expired(now time.Time) bool {
	return !s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt)
}
