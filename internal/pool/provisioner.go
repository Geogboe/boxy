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

// LockedProvisioner is an optional Provisioner capability for
// implementations that route through something ProvisionLocker can
// serialize against a concurrent observer (see ProvisionLocker's doc
// comment on the ghost-orphan race this exists to close). Manager prefers
// ProvisionLocked over Provision when the configured Provisioner implements
// it.
//
// Why this exists instead of Manager just locking around Provision itself:
// Manager doesn't know which agent (or other lock key) a given pool will
// resolve to until the provisioner resolves it — and for AgentProvisioner,
// that resolution is itself a stateful, non-repeatable operation
// (AgentRegistry.Resolve round-robins across every agent advertising a
// provider type, so resolving twice for one Provision call could select
// two different agents). Only the provisioner can correctly acquire a lock
// keyed to the exact same resolution its own Create call will use — so the
// lock must be acquired *inside* ProvisionLocked, not by Manager beforehand.
//
// persist is called at most once, synchronously, while any internal lock
// ProvisionLocked acquires is still held: on a successful Create, or on a
// Create failure that produced a quarantined (OrphanedResourceError)
// resource. It is never called for a plain Create failure with no resource
// to persist. persist may mutate *res in place (e.g. Manager sets
// pool-inventory/admission-specific fields) before writing it — the
// (possibly mutated) resource is what ProvisionLocked returns. If persist
// returns an error, ProvisionLocked returns that error instead of any
// Create error, matching the pre-existing precedence where a failed
// quarantine write was reported over the Create failure it was recording.
//
// created reports whether Create itself succeeded, independent of whether
// persist subsequently failed — Manager uses this (not err) to decide
// between recordProvisionSuccess and recordProvisionFailure, since a
// persist failure after a successful Create must not count against the
// pool's provisioning backoff the way a real Create failure does.
type LockedProvisioner interface {
	ProvisionLocked(ctx context.Context, pool model.Pool, persist func(*model.Resource) error) (res model.Resource, created bool, err error)
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
