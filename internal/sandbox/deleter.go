package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type ResourceDestroyer interface {
	DestroyResource(ctx context.Context, res model.Resource) error
}

// SegmentDestroyer is an optional capability for a ResourceDestroyer that
// also knows how to tear down network segments (see model.NetworkSegment).
// Not every destroyer supports this -- a deployment with no
// network-isolation-capable providers at all uses a plain
// ResourceDestroyer with no segments to ever destroy.
type SegmentDestroyer interface {
	DestroySegment(ctx context.Context, agentID string, providerType string, ref string) error
}

// DeletionReconciler cleans up sandboxes that have been accepted for async
// deletion, and promotes sandboxes past their Policies.AutoDestroyAfter
// expiry into deletion.
type DeletionReconciler struct {
	store     store.Store
	destroyer ResourceDestroyer
	clock     Clock
}

func NewDeletionReconciler(st store.Store, destroyer ResourceDestroyer) *DeletionReconciler {
	return &DeletionReconciler{store: st, destroyer: destroyer, clock: realClock{}}
}

// SetClock overrides the reconciler's time source. Used by tests.
func (r *DeletionReconciler) SetClock(c Clock) {
	if c != nil {
		r.clock = c
	}
}

func (r *DeletionReconciler) now() time.Time {
	if r.clock == nil {
		return time.Now().UTC()
	}
	return r.clock.Now()
}

func (r *DeletionReconciler) Reconcile(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("sandbox deletion reconciler is nil")
	}
	if r.store == nil {
		return fmt.Errorf("store is nil")
	}
	if r.destroyer == nil {
		return fmt.Errorf("resource destroyer is nil")
	}

	sandboxes, err := r.store.ListSandboxes(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	sort.Slice(sandboxes, func(i, j int) bool {
		return sandboxes[i].ID < sandboxes[j].ID
	})

	now := r.now()
	for _, sb := range sandboxes {
		if sb.Status != model.SandboxStatusDeleting {
			if sb.ExpiresAt == nil || sb.ExpiresAt.After(now) {
				continue
			}
			sb.Status = model.SandboxStatusDeleting
			sb.Error = ""
			if err := r.store.PutSandbox(ctx, sb); err != nil {
				return fmt.Errorf("mark expired sandbox %q deleting: %w", sb.ID, err)
			}
		}
		if err := r.cleanupSandbox(ctx, sb.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *DeletionReconciler) cleanupSandbox(ctx context.Context, id model.SandboxID) error {
	sb, err := r.store.GetSandbox(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get deleting sandbox %q: %w", id, err)
	}
	if sb.Status != model.SandboxStatusDeleting {
		return nil
	}

	for len(sb.Resources) > 0 {
		rid := sb.Resources[0]
		res, err := r.store.GetResource(ctx, rid)
		if errors.Is(err, store.ErrNotFound) {
			sb.Resources = removeResourceID(sb.Resources, rid)
			if err := r.store.PutSandbox(ctx, sb); err != nil {
				return fmt.Errorf("remove missing resource %q from sandbox %q: %w", rid, sb.ID, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("get resource %q for deleting sandbox %q: %w", rid, sb.ID, err)
		}
		if err := r.destroyer.DestroyResource(ctx, res); err != nil {
			return fmt.Errorf("cleanup resource %q for sandbox %q: %w", rid, sb.ID, err)
		}
		sb.Resources = removeResourceID(sb.Resources, rid)
		if err := r.store.PutSandbox(ctx, sb); err != nil {
			return fmt.Errorf("remove destroyed resource %q from sandbox %q: %w", rid, sb.ID, err)
		}
	}

	if segmentDestroyer, ok := r.destroyer.(SegmentDestroyer); ok {
		for _, seg := range sb.NetworkSegments {
			err := segmentDestroyer.DestroySegment(ctx, seg.AgentID, seg.ProviderType, seg.Ref)
			// An agent that is simply gone is not a teardown failure to
			// retry forever. Blocking here would keep this sandbox in
			// `deleting` indefinitely and -- because Reconcile returns on
			// the first cleanupSandbox error -- stall every later sandbox
			// in the same tick behind a host that is never coming back.
			// This mirrors the resource path's force-orphan escape hatch
			// in spirit: the record is released here, and reclaiming the
			// host-side object is the deferred segment orphan sweep's job.
			// Any other DestroySegment failure remains a hard error.
			if errors.Is(err, pool.ErrSegmentAgentUnavailable) {
				slog.Default().Warn("skipping network segment teardown; its agent is no longer registered",
					"operation", "sandbox_destroy_segment",
					"sandbox_id", sb.ID,
					"agent_id", seg.AgentID,
					"provider_type", seg.ProviderType,
					"segment_ref", seg.Ref,
					"error", err,
				)
				continue
			}
			if err != nil {
				return fmt.Errorf("destroy network segment %q for sandbox %q: %w", seg.Ref, sb.ID, err)
			}
		}
	}

	if err := r.store.DeleteSandbox(ctx, sb.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("delete cleaned sandbox %q: %w", sb.ID, err)
	}
	return nil
}

func removeResourceID(ids []model.ResourceID, id model.ResourceID) []model.ResourceID {
	out := ids[:0]
	for _, existing := range ids {
		if existing == id {
			continue
		}
		out = append(out, existing)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
