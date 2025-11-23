package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/pkg/provider"
)

// CreateSandboxRequest represents the incoming JSON payload.
type CreateSandboxRequest struct {
	Name      string                  `json:"name,omitempty"`
	Duration  string                  `json:"duration"`
	Resources []CreateResourceRequest `json:"resources"`
	Metadata  map[string]string       `json:"metadata,omitempty"`
}

// CreateResourceRequest captures pool/count pairs.
type CreateResourceRequest struct {
	Pool  string `json:"pool"`
	Count int    `json:"count"`
}

// ExtendSandboxRequest is used for extend operations.
type ExtendSandboxRequest struct {
	Duration string `json:"duration"`
}

// SandboxResponse is returned to clients.
type SandboxResponse struct {
	ID        string               `json:"id"`
	Name      string               `json:"name,omitempty"`
	State     sandbox.SandboxState `json:"state"`
	Resources []string             `json:"resources"`
	Metadata  map[string]string    `json:"metadata,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
	ExpiresAt *time.Time           `json:"expires_at,omitempty"`
	Details   []*ResourceResponse  `json:"details,omitempty"`
}

// ResourceResponse combines resource and connection info.
type ResourceResponse struct {
	ID           string                   `json:"id"`
	PoolID       string                   `json:"pool_id"`
	Type         provider.ResourceType    `json:"type"`
	State        provider.ResourceState   `json:"state"`
	ProviderType string                   `json:"provider_type"`
	Connection   *provider.ConnectionInfo `json:"connection,omitempty"`
	Metadata     map[string]interface{}   `json:"metadata,omitempty"`
}

// PoolStatus is returned for pool listings.
type PoolStatus struct {
	Name         string `json:"name"`
	Backend      string `json:"backend"`
	MinReady     int    `json:"min_ready"`
	MaxTotal     int    `json:"max_total"`
	Ready        int    `json:"ready"`
	Allocated    int    `json:"allocated"`
	Provisioning int    `json:"provisioning"`
	Destroying   int    `json:"destroying"`
	Error        int    `json:"error"`
}

// ToDomain converts API payload to sandbox.CreateRequest.
func (r *CreateSandboxRequest) ToDomain() (*sandbox.CreateRequest, error) {
	if r == nil {
		return nil, fmt.Errorf("request is required")
	}

	dur, err := time.ParseDuration(r.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}

	resources := make([]sandbox.ResourceRequest, 0, len(r.Resources))
	for _, res := range r.Resources {
		resources = append(resources, sandbox.ResourceRequest{
			PoolName: res.Pool,
			Count:    res.Count,
		})
	}

	return &sandbox.CreateRequest{
		Name:      r.Name,
		Resources: resources,
		Duration:  dur,
		Metadata:  r.Metadata,
	}, nil
}

// SandboxResponseFromDomain converts a sandbox to API response.
func SandboxResponseFromDomain(sb *sandbox.Sandbox, details []*ResourceResponse) *SandboxResponse {
	resp := &SandboxResponse{
		ID:        sb.ID,
		Name:      sb.Name,
		State:     sb.State,
		Resources: sb.ResourceIDs,
		Metadata:  sb.Metadata,
		CreatedAt: sb.CreatedAt,
		UpdatedAt: sb.UpdatedAt,
		ExpiresAt: sb.ExpiresAt,
		Details:   details,
	}
	return resp
}

// ResourcesToResponse converts resources with connection info.
func ResourcesToResponse(in []*sandbox.ResourceWithConnection) []*ResourceResponse {
	var out []*ResourceResponse
	for _, res := range in {
		out = append(out, &ResourceResponse{
			ID:           res.Resource.ID,
			PoolID:       res.Resource.PoolID,
			Type:         res.Resource.Type,
			State:        res.Resource.State,
			ProviderType: res.Resource.ProviderType,
			Connection:   res.Connection,
			Metadata:     res.Resource.Metadata,
		})
	}
	return out
}

// PoolStatFetcher is used in tests to mock pool stats.
type PoolStatFetcher func() ([]PoolStatus, error)

// List allows PoolStatFetcher to satisfy PoolStatsProvider.
func (f PoolStatFetcher) List() ([]PoolStatus, error) { return f() }

// SandboxServiceFunc allows lightweight mocking of SandboxService for tests.
type SandboxServiceFunc struct {
	CreateFn                 func(context.Context, *sandbox.CreateRequest) (*sandbox.Sandbox, error)
	ListFn                   func(context.Context) ([]*sandbox.Sandbox, error)
	GetFn                    func(context.Context, string) (*sandbox.Sandbox, error)
	DestroyFn                func(context.Context, string) error
	ExtendFn                 func(context.Context, string, time.Duration) error
	GetResourcesForSandboxFn func(context.Context, string) ([]*sandbox.ResourceWithConnection, error)
}

func (s SandboxServiceFunc) Create(ctx context.Context, req *sandbox.CreateRequest) (*sandbox.Sandbox, error) {
	return s.CreateFn(ctx, req)
}
func (s SandboxServiceFunc) List(ctx context.Context) ([]*sandbox.Sandbox, error) {
	return s.ListFn(ctx)
}
func (s SandboxServiceFunc) Get(ctx context.Context, id string) (*sandbox.Sandbox, error) {
	return s.GetFn(ctx, id)
}
func (s SandboxServiceFunc) Destroy(ctx context.Context, id string) error {
	return s.DestroyFn(ctx, id)
}
func (s SandboxServiceFunc) Extend(ctx context.Context, id string, d time.Duration) error {
	return s.ExtendFn(ctx, id, d)
}
func (s SandboxServiceFunc) GetResourcesForSandbox(ctx context.Context, sandboxID string) ([]*sandbox.ResourceWithConnection, error) {
	return s.GetResourcesForSandboxFn(ctx, sandboxID)
}
