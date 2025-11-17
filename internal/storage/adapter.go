package storage

import (
	"context"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/core/sandbox"
)

// ResourceRepositoryAdapter adapts Store to the pool.ResourceRepository interface
type ResourceRepositoryAdapter struct {
	store Store
}

// NewResourceRepositoryAdapter creates a new adapter
func NewResourceRepositoryAdapter(store Store) *ResourceRepositoryAdapter {
	return &ResourceRepositoryAdapter{store: store}
}

func (a *ResourceRepositoryAdapter) Create(ctx context.Context, res *resource.Resource) error {
	return a.store.CreateResource(ctx, res)
}

func (a *ResourceRepositoryAdapter) Update(ctx context.Context, res *resource.Resource) error {
	return a.store.UpdateResource(ctx, res)
}

func (a *ResourceRepositoryAdapter) Delete(ctx context.Context, id string) error {
	return a.store.DeleteResource(ctx, id)
}

func (a *ResourceRepositoryAdapter) GetByID(ctx context.Context, id string) (*resource.Resource, error) {
	return a.store.GetResourceByID(ctx, id)
}

func (a *ResourceRepositoryAdapter) GetByPoolID(ctx context.Context, poolID string) ([]*resource.Resource, error) {
	return a.store.GetResourcesByPoolID(ctx, poolID)
}

func (a *ResourceRepositoryAdapter) GetByState(ctx context.Context, poolID string, state resource.ResourceState) ([]*resource.Resource, error) {
	return a.store.GetResourcesByState(ctx, poolID, state)
}

func (a *ResourceRepositoryAdapter) CountByPoolAndState(ctx context.Context, poolID string, state resource.ResourceState) (int, error) {
	return a.store.CountResourcesByPoolAndState(ctx, poolID, state)
}

func (a *ResourceRepositoryAdapter) GetResourceByID(ctx context.Context, id string) (*resource.Resource, error) {
	return a.store.GetResourceByID(ctx, id)
}

func (a *ResourceRepositoryAdapter) GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*resource.Resource, error) {
	return a.store.GetResourcesBySandboxID(ctx, sandboxID)
}

func (a *ResourceRepositoryAdapter) UpdateResource(ctx context.Context, res *resource.Resource) error {
	return a.store.UpdateResource(ctx, res)
}

// SandboxRepositoryAdapter adapts Store to the sandbox.SandboxRepository interface
type SandboxRepositoryAdapter struct {
	store Store
}

// NewSandboxRepositoryAdapter creates a new adapter
func NewSandboxRepositoryAdapter(store Store) *SandboxRepositoryAdapter {
	return &SandboxRepositoryAdapter{store: store}
}

func (a *SandboxRepositoryAdapter) CreateSandbox(ctx context.Context, sb *sandbox.Sandbox) error {
	return a.store.CreateSandbox(ctx, sb)
}

func (a *SandboxRepositoryAdapter) UpdateSandbox(ctx context.Context, sb *sandbox.Sandbox) error {
	return a.store.UpdateSandbox(ctx, sb)
}

func (a *SandboxRepositoryAdapter) DeleteSandbox(ctx context.Context, id string) error {
	return a.store.DeleteSandbox(ctx, id)
}

func (a *SandboxRepositoryAdapter) GetSandboxByID(ctx context.Context, id string) (*sandbox.Sandbox, error) {
	return a.store.GetSandboxByID(ctx, id)
}

func (a *SandboxRepositoryAdapter) ListSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	return a.store.ListSandboxes(ctx)
}

func (a *SandboxRepositoryAdapter) ListActiveSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	return a.store.ListActiveSandboxes(ctx)
}

func (a *SandboxRepositoryAdapter) GetExpiredSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	return a.store.GetExpiredSandboxes(ctx)
}
