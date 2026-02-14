package pool

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

// Provisioner is the execution seam for the pool subsystem.
//
// What:
//   It creates and destroys individual resources for a specific pool.
//
// Why:
//   Pool.Manager should only enforce policy ("keep N Ready resources"), not know
//   how to talk to Docker/Hyper-V/etc. Provider-specific IO belongs behind this
//   seam (typically via internal/core/providers drivers and, later, agents).
//
// When:
//   Implement PoolProvisioner when wiring Boxy to real providers, or in tests.
//
// How:
//   - Provision should return a Resource that matches pool.Inventory.ExpectedType.
//   - If the resource is immediately usable, set Resource.State=ResourceStateReady.
//   - Destroy should be best-effort; on failure, Pool.Manager will surface the error.
type Provisioner interface {
	Provision(ctx context.Context, pool model.Pool) (model.Resource, error)
	Destroy(ctx context.Context, pool model.Pool, res model.Resource) error
}

// UnimplementedProvisioner is a safe default that fails fast with a clear error.
// It is useful for early wiring so nil pointers don't masquerade as "not configured".
type UnimplementedProvisioner struct{}

func (UnimplementedProvisioner) Provision(ctx context.Context, pool model.Pool) (model.Resource, error) {
	_ = ctx
	_ = pool
	return model.Resource{}, fmt.Errorf("pool provisioner not implemented")
}

func (UnimplementedProvisioner) Destroy(ctx context.Context, pool model.Pool, res model.Resource) error {
	_ = ctx
	_ = pool
	_ = res
	return fmt.Errorf("pool provisioner not implemented")
}
