package pool

import (
	"context"
	"fmt"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// Provisioner is the execution seam for the pool subsystem.
//
// What:
//
//	It creates and destroys individual resources for a specific pool.
//
// Why:
//
//	Pool.Manager should only enforce policy ("keep N Ready resources"), not know
//	how to talk to Docker/Hyper-V/etc. Provider-specific IO belongs behind this
//	seam (typically via provider drivers and, later, agents).
//
// When:
//
//	Implement PoolProvisioner when wiring Boxy to real providers, or in tests.
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

// newQuarantinedResource builds the ResourceStateError record written for a
// Create failure whose cleanup also failed (see #174). Shared by
// DriverProvisioner and AgentProvisioner, whose Provision methods were
// otherwise identical here except for how each resolves "now" (dp.Now/
// ap.Now vs. time.Now) — now is a parameter rather than this function
// reading a Now func itself, so it stays free of both provisioner types.
// agentID is empty for DriverProvisioner (no agent concept); AgentProvisioner
// passes its own.
func newQuarantinedResource(poolName model.PoolName, providerName, agentID string, orphanErr *providersdk.OrphanedResourceError, now time.Time) model.Resource {
	return model.Resource{
		ID:         model.ResourceID(orphanErr.ID),
		OriginPool: poolName,
		Provider:   model.ProviderRef{Name: providerName, AgentID: agentID},
		State:      model.ResourceStateError,
		Properties: map[string]any{"quarantine_reason": orphanErr.CauseMessage},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}
