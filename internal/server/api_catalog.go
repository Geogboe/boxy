package server

// APIRoute describes a documented REST route. Keep this catalog adjacent to
// route registration and regenerate docs/api.md after changing the API.
type APIRoute struct {
	Group       string
	Method      string
	Path        string
	Auth        string
	Description string
}

// APIRouteCatalog is the source of truth for the checked-in REST reference.
func APIRouteCatalog() []APIRoute {
	return []APIRoute{
		{Group: "Health", Method: "GET", Path: "/healthz", Auth: "none", Description: "Return ok when the HTTP server is alive."},
		{Group: "API keys", Method: "POST", Path: "/api/v1/api-keys/bootstrap", Auth: "loopback", Description: "Create the first administrator key once; raw value is returned once."},
		{Group: "API keys", Method: "POST", Path: "/api/v1/api-keys", Auth: "admin", Description: "Create a service key for a user, auditor, or admin role; raw value is returned once."},
		{Group: "API keys", Method: "GET", Path: "/api/v1/api-keys", Auth: "admin", Description: "List service-key metadata without hashes, personal keys, or raw values."},
		{Group: "API keys", Method: "DELETE", Path: "/api/v1/api-keys/{id}", Auth: "admin", Description: "Revoke a service key; repeated revocation is idempotent."},
		{Group: "API keys", Method: "POST", Path: "/api/v1/api-keys/oidc-exchange", Auth: "id_token", Description: "Exchange a verified OIDC ID token (from `boxy login --oidc`) for a self-service personal API key; raw value is returned once."},
		{Group: "Pools", Method: "GET", Path: "/api/v1/pools", Auth: "auditor/admin", Description: "List configured pools and ready inventory."},
		{Group: "Pools", Method: "GET", Path: "/api/v1/pools/{name}", Auth: "auditor/admin", Description: "Inspect one pool."},
		{Group: "Pools", Method: "POST", Path: "/api/v1/pools/{name}/drain", Auth: "admin", Description: "Drain unused ready inventory."},
		{Group: "Pools", Method: "POST", Path: "/api/v1/pools/{name}/fill", Auth: "admin", Description: "Reconcile a pool to its configured target."},
		{Group: "Pools", Method: "POST", Path: "/api/v1/pools/{name}/guest-credential", Auth: "admin", Description: "Set a pool's guest bootstrap credential from a request body; the raw value is never returned."},
		{Group: "Resources", Method: "GET", Path: "/api/v1/resources", Auth: "auditor/admin", Description: "List resources."},
		{Group: "Resources", Method: "GET", Path: "/api/v1/resources/{id}", Auth: "auditor/admin", Description: "Inspect one resource."},
		{Group: "Sandboxes", Method: "GET", Path: "/api/v1/sandboxes", Auth: "user/auditor/admin", Description: "List owned sandboxes for users; all for auditors/admins."},
		{Group: "Sandboxes", Method: "GET", Path: "/api/v1/sandboxes/{id}", Auth: "user/auditor/admin", Description: "Inspect a sandbox, subject to user ownership."},
		{Group: "Sandboxes", Method: "POST", Path: "/api/v1/sandboxes", Auth: "user/admin", Description: "Create an owned asynchronous sandbox request."},
		{Group: "Sandboxes", Method: "DELETE", Path: "/api/v1/sandboxes/{id}", Auth: "user/admin", Description: "Request asynchronous deletion."},
		{Group: "Sandboxes", Method: "POST", Path: "/api/v1/sandboxes/{id}/extend", Auth: "user/admin", Description: "Extend an owned sandbox expiry."},
		{Group: "Sandboxes", Method: "GET", Path: "/api/v1/sandboxes/{id}/guest-credential", Auth: "user/admin", Description: "Fetch process-local guest credentials once; subsequent fetches return 410 Gone."},
		{Group: "Sandboxes", Method: "POST", Path: "/api/v1/sandboxes/{id}/exec", Auth: "user/admin", Description: "Execute a one-shot command; use stream=true for NDJSON events."},
		{Group: "Agents", Method: "POST", Path: "/api/v1/agent-tokens", Auth: "admin", Description: "Mint a single-use remote-agent registration token."},
		{Group: "Agents", Method: "GET", Path: "/api/v1/agent-tokens", Auth: "admin", Description: "List registration-token metadata."},
		{Group: "Agents", Method: "DELETE", Path: "/api/v1/agent-tokens/{id}", Auth: "admin", Description: "Revoke an unused registration token."},
		{Group: "Agents", Method: "GET", Path: "/api/v1/agents", Auth: "auditor/admin", Description: "List registered agents, connection state, heartbeat time, and capacity samples."},
		{Group: "Agents", Method: "DELETE", Path: "/api/v1/agents/{id}", Auth: "admin", Description: "Revoke an agent identity."},
		{Group: "Diagnostics", Method: "GET", Path: "/api/v1/diagnostics/logs", Auth: "admin", Description: "Query bounded, redacted control-plane and server-observed agent diagnostics."},
	}
}
