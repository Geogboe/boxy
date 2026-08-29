package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/resourcepack"
	"github.com/Geogboe/boxy/pkg/store"
)

type promotionProvisioner struct {
	destroyed  []model.ResourceID
	destroyErr error
	compatible bool
}

func (p *promotionProvisioner) Provision(context.Context, model.Pool) (model.Resource, error) {
	return model.Resource{}, errors.New("not used")
}

func (p *promotionProvisioner) Destroy(_ context.Context, _ model.Pool, res model.Resource) error {
	p.destroyed = append(p.destroyed, res.ID)
	return p.destroyErr
}

func (p *promotionProvisioner) CompatibleWithPool(model.Pool, model.Resource) bool {
	return p.compatible
}

type promotionPackageApplier struct {
	events []resourcepack.Event
	err    error
}

func (p *promotionPackageApplier) ApplyResourcePackages(_ context.Context, _ model.Pool, _ model.Resource, event resourcepack.Event) ([]resourcepack.AppliedPackage, error) {
	p.events = append(p.events, event)
	if p.err != nil {
		return nil, p.err
	}
	return []resourcepack.AppliedPackage{{Reference: "base@1.0.0", InputDigest: "digest"}}, nil
}

func promotionPool(name, template string, minReady int) model.Pool {
	return model.Pool{
		Name:     model.PoolName(name),
		Template: template,
		Policies: model.PoolPolicies{Preheat: model.PreheatPolicy{MinReady: minReady}},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeVM,
			ExpectedProfile: model.ResourceProfile("windows"),
		},
	}
}

func TestPromotionServicePromotesAfterDestinationPackagesSucceed(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	source := promotionPool("base", "base", 0)
	destination := promotionPool("apps", "apps", 1)
	resource := model.Resource{
		ID:          "vm-1",
		Type:        model.ResourceTypeVM,
		Profile:     "windows",
		OriginPool:  source.Name,
		CurrentPool: source.Name,
		State:       model.ResourceStateReady,
	}
	if err := st.PutPool(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := st.PutPool(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := st.PutResource(ctx, resource); err != nil {
		t.Fatal(err)
	}

	provisioner := &promotionProvisioner{compatible: true}
	packages := &promotionPackageApplier{}
	service := &PromotionService{
		Store:           st,
		Provisioner:     provisioner,
		Compatibility:   provisioner,
		Packages:        packages,
		TemplateParents: map[string]string{"apps": "base"},
		Clock:           fixedClock{t: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)},
	}
	if err := service.Promote(ctx, destination.Name, 0); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	got, err := st.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.ResourceStateReady || got.CurrentPool != destination.Name || got.PendingPool != "" {
		t.Fatalf("promoted resource = %+v", got)
	}
	if len(got.AppliedPackages) != 1 || got.AppliedPackages[0].Reference != "base@1.0.0" {
		t.Fatalf("applied packages = %+v", got.AppliedPackages)
	}
	if len(packages.events) != 1 || packages.events[0] != resourcepack.EventPromotion {
		t.Fatalf("package events = %+v", packages.events)
	}
	if len(provisioner.destroyed) != 0 {
		t.Fatalf("destroyed resources = %+v", provisioner.destroyed)
	}

	gotSource, err := st.GetPool(ctx, source.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSource.Inventory.Resources) != 0 {
		t.Fatalf("source inventory = %+v", gotSource.Inventory.Resources)
	}
	gotDestination, err := st.GetPool(ctx, destination.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotDestination.Inventory.Resources) != 1 || gotDestination.Inventory.Resources[0].ID != resource.ID {
		t.Fatalf("destination inventory = %+v", gotDestination.Inventory.Resources)
	}
}

func TestPromotionServicePreservesSourceMinimumReady(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	source := promotionPool("base", "base", 1)
	destination := promotionPool("apps", "apps", 1)
	resource := model.Resource{ID: "vm-1", Type: model.ResourceTypeVM, Profile: "windows", OriginPool: source.Name, State: model.ResourceStateReady}
	for _, pool := range []model.Pool{source, destination} {
		if err := st.PutPool(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutResource(ctx, resource); err != nil {
		t.Fatal(err)
	}
	provisioner := &promotionProvisioner{}
	service := &PromotionService{Store: st, Provisioner: provisioner, Compatibility: provisioner, TemplateParents: map[string]string{"apps": "base"}}
	if err := service.Promote(ctx, destination.Name, 0); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(provisioner.destroyed) != 0 {
		t.Fatalf("destroyed resources = %+v", provisioner.destroyed)
	}
	got, err := st.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectivePool() != source.Name || got.State != model.ResourceStateReady {
		t.Fatalf("resource changed despite source minimum = %+v", got)
	}
}

func TestPromotionServiceDestroysResourceWhenPackageApplicationFails(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	source := promotionPool("base", "base", 0)
	destination := promotionPool("apps", "apps", 1)
	resource := model.Resource{ID: "vm-1", Type: model.ResourceTypeVM, Profile: "windows", OriginPool: source.Name, State: model.ResourceStateReady}
	for _, pool := range []model.Pool{source, destination} {
		if err := st.PutPool(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutResource(ctx, resource); err != nil {
		t.Fatal(err)
	}
	provisioner := &promotionProvisioner{compatible: true}
	service := &PromotionService{
		Store:           st,
		Provisioner:     provisioner,
		Compatibility:   provisioner,
		Packages:        &promotionPackageApplier{err: errors.New("package failed")},
		TemplateParents: map[string]string{"apps": "base"},
	}
	if err := service.Promote(ctx, destination.Name, 0); err == nil {
		t.Fatal("Promote succeeded despite package failure")
	}
	if len(provisioner.destroyed) != 1 || provisioner.destroyed[0] != resource.ID {
		t.Fatalf("destroyed resources = %+v", provisioner.destroyed)
	}
	if _, err := st.GetResource(ctx, resource.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("resource lookup error = %v, want not found", err)
	}
}

func TestPromotionServiceSkipsIncompatibleProvider(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	source := promotionPool("base", "base", 0)
	destination := promotionPool("apps", "apps", 1)
	resource := model.Resource{
		ID:          "vm-1",
		Type:        model.ResourceTypeVM,
		Profile:     "windows",
		OriginPool:  source.Name,
		CurrentPool: source.Name,
		Provider:    model.ProviderRef{Name: "hyperv"},
		State:       model.ResourceStateReady,
	}
	for _, pool := range []model.Pool{source, destination} {
		if err := st.PutPool(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutResource(ctx, resource); err != nil {
		t.Fatal(err)
	}

	provisioner := &promotionProvisioner{}
	service := &PromotionService{
		Store:           st,
		Provisioner:     provisioner,
		Compatibility:   provisioner,
		TemplateParents: map[string]string{"apps": "base"},
	}
	if err := service.Promote(ctx, destination.Name, 0); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(provisioner.destroyed) != 0 {
		t.Fatalf("destroyed resources = %+v", provisioner.destroyed)
	}
	got, err := st.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentPool != source.Name || got.State != model.ResourceStateReady {
		t.Fatalf("incompatible resource changed = %+v", got)
	}
}
