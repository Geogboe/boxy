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
		{Group: "API keys", Method: "POST", Path: "/api/v1/api-keys", Auth: "admin", Description: "Create a user, auditor, or admin key; raw value is returned once."},
		{Group: "API keys", Method: "GET", Path: "/api/v1/api-keys", Auth: "admin", Description: "List key metadata without hashes or raw values."},
		{Group: "API keys", Method: "DELETE", Path: "/api/v1/api-keys/{id}", Auth: "admin", Description: "Revoke an API key."},
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
		{Group: "Agents", Method: "GET", Path: "/api/v1/agents", Auth: "auditor/admin", Description: "List registered agents."},
		{Group: "Agents", Method: "DELETE", Path: "/api/v1/agents/{id}", Auth: "admin", Description: "Revoke an agent identity."},
	}
}
