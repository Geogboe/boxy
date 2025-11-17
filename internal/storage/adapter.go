package storage

import (
	"context"

	"github.com/Geogboe/boxy/internal/core/resource"
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
