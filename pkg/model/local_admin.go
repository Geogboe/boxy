package model

import "time"

// LocalAdminUsername is the fixed username of the single bootstrapped local
// administrator account (see LocalAdminAccount). Not configurable in v1 —
// there is exactly one local-admin account per daemon.
const LocalAdminUsername = "admin"

// LocalAdminAccount is the daemon's single bootstrapped local administrator
// account, used to log into the web UI (SessionKindLocalAdmin) when no OIDC
// provider is configured, or as a break-glass login even when one is. Only
// PasswordHash is persisted; the raw bootstrap password is generated once,
// written to a restricted-permission file for one-time CLI retrieval, and
// never stored here — the same "hash persisted, raw value shown once"
// discipline as APIKey/api-key bootstrap.
type LocalAdminAccount struct {
	Username     string    `json:"username" yaml:"username"`
	PasswordHash string    `json:"password_hash" yaml:"password_hash"`
	CreatedAt    time.Time `json:"created_at" yaml:"created_at"`
}
