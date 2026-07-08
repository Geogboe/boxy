package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

type ResourceDestroyer interface {
	DestroyResource(ctx context.Context, res model.Resource) error
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
