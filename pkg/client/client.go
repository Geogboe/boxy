package client

import (
	"context"
	"time"
)

// TODO: Implement boxy HTTP API client
//
// Implementation checklist:
// [ ] Client struct with base URL and HTTP client
// [ ] Authentication support (if needed)
// [ ] CreateSandbox method
// [ ] GetSandbox method
// [ ] ListSandboxes method
// [ ] DestroySandbox method
// [ ] ExtendSandbox method
// [ ] GetSandboxResources method
// [ ] WaitForSandbox helper
// [ ] GetPoolStats method
// [ ] ListPools method
// [ ] Error handling and retries
// [ ] Request/response types matching API
// [ ] Examples and documentation

// Client provides methods to interact with a boxy server.
type Client struct {
	baseURL string
	// httpClient *http.Client
}

// New creates a new boxy API client.
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
	}
}

// CreateSandboxRequest represents a sandbox creation request.
type CreateSandboxRequest struct {
	Name      string            `json:"name"`
	Resources []ResourceRequest `json:"resources"`
	Duration  time.Duration     `json:"duration"`
}

// ResourceRequest specifies resources to allocate for a sandbox.
type ResourceRequest struct {
	PoolName string `json:"pool_name"`
	Count    int    `json:"count"`
}

// Sandbox represents a sandbox response.
type Sandbox struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	State       string    `json:"state"`
	ResourceIDs []string  `json:"resource_ids"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateSandbox creates a new sandbox.
func (c *Client) CreateSandbox(ctx context.Context, req *CreateSandboxRequest) (*Sandbox, error) {
	// TODO: Implement HTTP POST to /sandboxes
	panic("not implemented")
}

// GetSandbox retrieves a sandbox by ID.
func (c *Client) GetSandbox(ctx context.Context, id string) (*Sandbox, error) {
	// TODO: Implement HTTP GET to /sandboxes/{id}
	panic("not implemented")
}

// ListSandboxes lists all sandboxes.
func (c *Client) ListSandboxes(ctx context.Context) ([]*Sandbox, error) {
	// TODO: Implement HTTP GET to /sandboxes
	panic("not implemented")
}

// DestroySandbox destroys a sandbox.
func (c *Client) DestroySandbox(ctx context.Context, id string) error {
	// TODO: Implement HTTP DELETE to /sandboxes/{id}
	panic("not implemented")
}

// WaitForSandbox waits for a sandbox to reach ready state.
func (c *Client) WaitForSandbox(ctx context.Context, id string, timeout time.Duration) (*Sandbox, error) {
	// TODO: Implement polling logic with exponential backoff
	panic("not implemented")
}
