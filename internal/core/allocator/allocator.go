package allocator

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/Geogboe/boxy/pkg/provider"
)

// PoolAllocator is the minimal interface the allocator needs from a pool manager.
type PoolAllocator interface {
	Allocate(ctx context.Context, sandboxID string, expiresAt *time.Time) (*provider.Resource, error)
	Release(ctx context.Context, resourceID string) error
}

// ResourceRepository exposes the resource lookups the allocator needs.
type ResourceRepository interface {
	GetResourceByID(ctx context.Context, id string) (*provider.Resource, error)
	GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*provider.Resource, error)
}

// Allocator orchestrates pool → sandbox movements without exposing pool internals.
type Allocator struct {
	pools        map[string]PoolAllocator
	resourceRepo ResourceRepository
	logger       *logrus.Logger
}

// New creates a new Allocator.
func New(pools map[string]PoolAllocator, resourceRepo ResourceRepository, logger *logrus.Logger) *Allocator {
	return &Allocator{
		pools:        pools,
		resourceRepo: resourceRepo,
		logger:       logger,
	}
}

// HasPool reports whether a pool with the given name is registered.
func (a *Allocator) HasPool(name string) bool {
	_, ok := a.pools[name]
	return ok
}

// Allocate reserves a resource from the named pool for the sandbox.
func (a *Allocator) Allocate(ctx context.Context, poolName, sandboxID string, expiresAt *time.Time) (*provider.Resource, error) {
	pool, ok := a.pools[poolName]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", poolName)
	}

	res, err := pool.Allocate(ctx, sandboxID, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("allocate from pool %s: %w", poolName, err)
	}

	return res, nil
}

// ReleaseResource destroys a single resource by looking up its owning pool.
func (a *Allocator) ReleaseResource(ctx context.Context, resourceID string) error {
	res, err := a.resourceRepo.GetResourceByID(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("lookup resource %s: %w", resourceID, err)
	}

	pool, ok := a.pools[res.PoolID]
	if !ok {
		return fmt.Errorf("pool %s not found for resource %s", res.PoolID, resourceID)
	}

	if err := pool.Release(ctx, resourceID); err != nil {
		return fmt.Errorf("release resource %s: %w", resourceID, err)
	}

	return nil
}

// ReleaseSandbox releases all resources owned by the given sandbox.
func (a *Allocator) ReleaseSandbox(ctx context.Context, sandboxID string) error {
	resources, err := a.resourceRepo.GetResourcesBySandboxID(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("list resources for sandbox %s: %w", sandboxID, err)
	}

	var releaseErrors []error
	for _, res := range resources {
		if err := a.ReleaseResource(ctx, res.ID); err != nil {
			releaseErrors = append(releaseErrors, err)
			a.logger.WithError(err).WithFields(logrus.Fields{
				"sandbox_id":  sandboxID,
				"resource_id": res.ID,
			}).Error("Failed to release resource")
		}
	}

	if len(releaseErrors) > 0 {
		return fmt.Errorf("failed to release %d resource(s) for sandbox %s", len(releaseErrors), sandboxID)
	}

	return nil
}
