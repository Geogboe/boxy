package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/resourcepack"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

// PromotionService implements the deliberately small promotion seam. It
// chooses a ready surplus resource from a pool using the nearest template
// ancestor, applies the destination package delta, and commits ownership only
// after the destination is usable.
type PromotionService struct {
	Store        store.Store
	Provisioner  Provisioner
	Packages     ResourcePackageApplier
	Personalizer GuestAdmissionPersonalizer
	Secrets      boxysecrets.Store

	// TemplateParents maps a template name to its single parent template.
	TemplateParents map[string]string
	Clock           Clock
	lockPool        func(model.PoolName) func()
}

// SetPoolLocker gives promotion the manager's existing per-pool lock. It is
// intentionally an unexported field so callers use Manager.SetPromoter.
func (p *PromotionService) SetPoolLocker(lock func(model.PoolName) func()) {
	if p != nil {
		p.lockPool = lock
	}
}

// Promote performs at most one promotion per reconciliation pass. Keeping the
// unit small limits lock duration and lets the normal reconciliation loop
// continue to enforce destination policy between moves.
func (p *PromotionService) Promote(ctx context.Context, destinationName model.PoolName, requestedReady int) error {
	if p == nil || p.Store == nil {
		return fmt.Errorf("promotion store is required")
	}
	if p.Provisioner == nil {
		return fmt.Errorf("promotion provisioner is required")
	}
	destination, err := p.Store.GetPool(ctx, destinationName)
	if err != nil {
		return fmt.Errorf("get destination pool: %w", err)
	}
	if strings.TrimSpace(destination.Template) == "" {
		return nil
	}

	resources, err := p.Store.ListResources(ctx)
	if err != nil {
		return fmt.Errorf("list resources for promotion: %w", err)
	}
	targetReady := max(requestedReady, destination.Policies.Preheat.MinReady)
	if readyOwned(resources, destination.Name) >= targetReady {
		return nil
	}
	if destination.Policies.Preheat.MaxTotal > 0 && nonDestroyedOwned(resources, destination.Name) >= destination.Policies.Preheat.MaxTotal {
		return nil
	}

	pools, err := p.Store.ListPools(ctx)
	if err != nil {
		return fmt.Errorf("list pools for promotion: %w", err)
	}
	source, candidate, ok := p.selectCandidate(destination, pools, resources)
	if !ok {
		return nil
	}
	if p.lockPool != nil {
		unlock := p.lockPool(source.Name)
		defer unlock()
	}
	return p.promoteOne(ctx, source, destination, candidate)
}

func (p *PromotionService) selectCandidate(destination model.Pool, pools []model.Pool, resources []model.Resource) (model.Pool, model.Resource, bool) {
	ancestor := p.TemplateParents[destination.Template]
	for ancestor != "" {
		candidates := make([]model.Pool, 0)
		for _, candidate := range pools {
			if candidate.Name != destination.Name && candidate.Template == ancestor {
				candidates = append(candidates, candidate)
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
		for _, source := range candidates {
			ready := make([]model.Resource, 0)
			for _, resource := range resources {
				if resource.EffectivePool() == source.Name && resource.State == model.ResourceStateReady && matchesPoolShape(destination, resource) {
					ready = append(ready, resource)
				}
			}
			if len(ready) <= source.Policies.Preheat.MinReady {
				continue
			}
			sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
			return source, ready[0], true
		}
		ancestor = p.TemplateParents[ancestor]
	}
	return model.Pool{}, model.Resource{}, false
}

func (p *PromotionService) promoteOne(ctx context.Context, source, destination model.Pool, candidate model.Resource) error {
	res, err := p.Store.GetResource(ctx, candidate.ID)
	if err != nil {
		return fmt.Errorf("get promotion resource %q: %w", candidate.ID, err)
	}
	if res.State != model.ResourceStateReady || res.EffectivePool() != source.Name {
		return nil
	}

	now := p.now()
	res.State = model.ResourceStatePromoting
	res.CurrentPool = source.Name
	res.PendingPool = destination.Name
	res.UpdatedAt = now
	if err := p.Store.PutResource(ctx, res); err != nil {
		return fmt.Errorf("mark resource %q promoting: %w", res.ID, err)
	}
	source.Inventory.Resources = removeInventoryResource(source.Inventory.Resources, res.ID)
	if err := p.Store.PutPool(ctx, source); err != nil {
		return fmt.Errorf("remove resource %q from source pool: %w", res.ID, err)
	}

	if err := p.rotateCredential(ctx, destination, &res); err != nil {
		return p.failPromotion(ctx, source, res, err)
	}
	if p.Packages != nil {
		applied, applyErr := p.Packages.ApplyResourcePackages(ctx, destination, res, resourcepack.EventPromotion)
		if applyErr != nil {
			return p.failPromotion(ctx, source, res, applyErr)
		}
		res.AppliedPackages = append(res.AppliedPackages, applied...)
	}

	res.State = model.ResourceStateReady
	res.CurrentPool = destination.Name
	res.PendingPool = ""
	res.UpdatedAt = p.now()
	if err := p.Store.PutResource(ctx, res); err != nil {
		return fmt.Errorf("commit promoted resource %q: %w", res.ID, err)
	}
	destination.Inventory.Resources = removeInventoryResource(destination.Inventory.Resources, res.ID)
	if err := destination.Inventory.Add(res); err != nil {
		return p.failPromotion(ctx, source, res, fmt.Errorf("add promoted resource to destination inventory: %w", err))
	}
	if err := p.Store.PutPool(ctx, destination); err != nil {
		return fmt.Errorf("store destination pool after promotion: %w", err)
	}
	return nil
}

func (p *PromotionService) rotateCredential(ctx context.Context, destination model.Pool, res *model.Resource) error {
	if p.Personalizer == nil {
		return nil
	}
	supports, err := p.Personalizer.SupportsGuestPersonalization(ctx, destination, *res)
	if err != nil {
		return fmt.Errorf("check destination guest personalization: %w", err)
	}
	if !supports {
		return nil
	}
	if p.Secrets == nil {
		return fmt.Errorf("secret backend is required for promotion guest credential")
	}
	result, err := p.Personalizer.PersonalizeGuestForPool(ctx, destination, *res)
	if err != nil {
		return fmt.Errorf("personalize resource for destination pool: %w", err)
	}
	if result == nil {
		return nil
	}
	if result.EphemeralCredential == nil || len(result.EphemeralCredential.Data) == 0 {
		return fmt.Errorf("destination personalization returned no credential")
	}
	credential, err := json.Marshal(result.EphemeralCredential)
	if err != nil {
		return fmt.Errorf("encode promotion credential: %w", err)
	}
	if err := p.Secrets.Put(ctx, boxysecrets.ResourceCredentialKey(string(res.ID)), credential); err != nil {
		return fmt.Errorf("store promotion credential: %w", err)
	}
	if properties := result.AccessDetails.ToProperties(); properties != nil {
		if res.Properties == nil {
			res.Properties = make(map[string]any)
		}
		for key, value := range properties {
			res.Properties[key] = value
		}
	}
	return nil
}

func (p *PromotionService) failPromotion(ctx context.Context, source model.Pool, res model.Resource, cause error) error {
	if err := p.Provisioner.Destroy(ctx, source, res); err == nil {
		if deleteErr := p.Store.DeleteResource(ctx, res.ID); deleteErr != nil && !errors.Is(deleteErr, store.ErrNotFound) {
			return errors.Join(cause, fmt.Errorf("delete failed promotion resource %q: %w", res.ID, deleteErr))
		}
		if p.Secrets != nil {
			_ = p.Secrets.Delete(ctx, boxysecrets.ResourceCredentialKey(string(res.ID)))
		}
		return fmt.Errorf("promotion resource %q destroyed: %w", res.ID, cause)
	} else {
		res.State = model.ResourceStateError
		res.CurrentPool = source.Name
		res.PendingPool = ""
		if res.Properties == nil {
			res.Properties = make(map[string]any)
		}
		res.Properties["promotion_error"] = cause.Error()
		res.UpdatedAt = p.now()
		if storeErr := p.Store.PutResource(ctx, res); storeErr != nil {
			return errors.Join(cause, err, storeErr)
		}
		return errors.Join(cause, fmt.Errorf("destroy failed promotion resource %q: %w", res.ID, err))
	}
}

func (p *PromotionService) now() time.Time {
	if p.Clock != nil {
		return p.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

func readyOwned(resources []model.Resource, pool model.PoolName) int {
	count := 0
	for _, resource := range resources {
		if resource.EffectivePool() == pool && resource.State == model.ResourceStateReady {
			count++
		}
	}
	return count
}

func nonDestroyedOwned(resources []model.Resource, pool model.PoolName) int {
	count := 0
	for _, resource := range resources {
		if resource.EffectivePool() == pool && resource.State != model.ResourceStateDestroyed {
			count++
		}
	}
	return count
}
