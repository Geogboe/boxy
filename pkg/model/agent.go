package model

import "time"

// AgentTokenID identifies a registration token record.
type AgentTokenID string

// AgentRegistrationToken is a single-use, short-lived bootstrap credential
// minted by an operator (`boxy agent token create`) and consumed exactly
// once by `boxy agent serve --token ...` during initial registration. The
// raw token is never persisted — only its hash, so a store dump does not
// itself let someone impersonate an agent.
type AgentRegistrationToken struct {
	ID        AgentTokenID `json:"id" yaml:"id"`
	TokenHash string       `json:"token_hash" yaml:"token_hash"`
	CreatedAt time.Time    `json:"created_at" yaml:"created_at"`
	ExpiresAt time.Time    `json:"expires_at" yaml:"expires_at"`
	UsedAt    *time.Time   `json:"used_at,omitempty" yaml:"used_at,omitempty"`
	Label     string       `json:"label,omitempty" yaml:"label,omitempty"`
}

// Used reports whether this token has already been redeemed.
func (t AgentRegistrationToken) Used() bool {
	return t.UsedAt != nil
}

// Expired reports whether this token's expiry has passed as of now.
func (t AgentRegistrationToken) Expired(now time.Time) bool {
	return now.After(t.ExpiresAt)
}

// AgentIdentity records which client certificate serial is currently
// associated with a registered agent, so an operator can revoke an agent
// by ID (`boxy agent revoke <id>`) even while it's disconnected — without
// this, the server would only learn an agent's cert serial while it has a
// live connection open.
type AgentIdentity struct {
	AgentID    string    `json:"agent_id" yaml:"agent_id"`
	CertSerial string    `json:"cert_serial" yaml:"cert_serial"`
	IssuedAt   time.Time `json:"issued_at" yaml:"issued_at"`
}

// AgentIdentityID identifies a revoked-agent-identity record.
type AgentIdentityID string

// RevokedAgentIdentity is a deny-list entry: once an agent's client
// certificate serial is revoked, the server must refuse new connections
// presenting that certificate, regardless of expiry, and tear down any
// live connection using it.
type RevokedAgentIdentity struct {
	ID         AgentIdentityID `json:"id" yaml:"id"`
	AgentID    string          `json:"agent_id" yaml:"agent_id"`
	CertSerial string          `json:"cert_serial" yaml:"cert_serial"`
	RevokedAt  time.Time       `json:"revoked_at" yaml:"revoked_at"`
	Reason     string          `json:"reason,omitempty" yaml:"reason,omitempty"`
}
