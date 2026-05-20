package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

type driverProvisionerConfig struct {
	Image string `json:"image"`
}

type fakeProviderDriver struct {
	createCfg      any
	deleted        []string
	allocated      []string
	personalized   []string
	deleteErr      error
	personalizeErr error
	personalize    bool
}

func (d *fakeProviderDriver) Type() providersdk.Type { return "fake" }

func (d *fakeProviderDriver) Create(ctx context.Context, cfg any) (*providersdk.Resource, error) {
	_ = ctx
	d.createCfg = cfg
	image, _ := cfg.(map[string]any)["image"].(string)
	return &providersdk.Resource{
		ID:             "provider-res-1",
		ConnectionInfo: map[string]string{"host": "127.0.0.1"},
		Metadata:       map[string]string{"image": image},
	}, nil
}

func (d *fakeProviderDriver) Read(ctx context.Context, id string) (*providersdk.ResourceStatus, error) {
	_ = ctx
	return &providersdk.ResourceStatus{ID: id, State: "running"}, nil
}

func (d *fakeProviderDriver) Update(ctx context.Context, id string, op providersdk.Operation) (*providersdk.Result, error) {
	_ = ctx
	_ = id
	_ = op
	return &providersdk.Result{}, nil
}

func (d *fakeProviderDriver) Delete(ctx context.Context, id string) error {
	_ = ctx
	d.deleted = append(d.deleted, id)
	return d.deleteErr
}

func (d *fakeProviderDriver) Allocate(ctx context.Context, id string) (map[string]any, error) {
	_ = ctx
	d.allocated = append(d.allocated, id)
	return map[string]any{"allocated": id}, nil
}

func (d *fakeProviderDriver) PersonalizeGuest(ctx context.Context, id string) (*providersdk.GuestPersonalizationResult, error) {
	_ = ctx
	d.personalized = append(d.personalized, id)
	if d.personalizeErr != nil {
		return nil, d.personalizeErr
	}
	if !d.personalize {
		return nil, nil
	}
	return &providersdk.GuestPersonalizationResult{
		AccessDetails: providersdk.GuestAccessDetails{Properties: map[string]string{"ssh_host": "10.0.0.5"}},
	}, nil
}

func newDriverProvisioner(t *testing.T, driver *fakeProviderDriver) *DriverProvisioner {
	t.Helper()
	reg := providersdk.NewRegistry()
	if err := reg.Register(providersdk.Registration{
		Type:        "fake",
		ConfigProto: func() any { return &driverProvisionerConfig{} },
		NewDriver: func(cfg any) (providersdk.Driver, error) {
			driver.createCfg = cfg
			return driver, nil
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return &DriverProvisioner{
		Registry: reg,
		Specs: map[model.PoolName]boxyconfig.PoolSpec{
			"web": {Name: "web", Type: "container", Provider: "fake-local", Config: map[string]any{"image": "alpine"}},
		},
		Providers: map[string]providersdk.Instance{
			"fake-local": {Name: "fake-local", Type: "fake"},
		},
		Now: func() time.Time { return time.Unix(123, 0) },
	}
}

func TestDriverProvisioner_ProvisionMapsProviderResourceToPoolResource(t *testing.T) {
	driver := &fakeProviderDriver{}
	dp := newDriverProvisioner(t, driver)

	res, err := dp.Provision(context.Background(), model.Pool{
		Name: "web",
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: "alpine",
		},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if res.ID != "provider-res-1" || res.OriginPool != "web" || res.Provider.Name != "fake-local" {
		t.Fatalf("resource = %+v, want provider resource tied to web/fake-local", res)
	}
	if res.Type != model.ResourceTypeContainer || res.Profile != "alpine" || res.State != model.ResourceStateReady {
		t.Fatalf("resource shape = %+v, want ready container alpine", res)
	}
	if res.Properties["host"] != "127.0.0.1" || res.Properties["image"] != "alpine" {
		t.Fatalf("properties = %+v, want connection info and metadata", res.Properties)
	}
	if cfg, ok := driver.createCfg.(map[string]any); !ok || cfg["image"] != "alpine" {
		t.Fatalf("driver create config = %#v, want pool image config", driver.createCfg)
	}
}

func TestDriverProvisioner_DestroyCallsProviderDelete(t *testing.T) {
	driver := &fakeProviderDriver{}
	dp := newDriverProvisioner(t, driver)

	err := dp.Destroy(context.Background(), model.Pool{Name: "web"}, model.Resource{ID: "provider-res-1"})
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(driver.deleted) != 1 || driver.deleted[0] != "provider-res-1" {
		t.Fatalf("deleted = %v, want provider-res-1", driver.deleted)
	}
}

func TestDriverProvisioner_DestroySurfacesProviderDeleteFailure(t *testing.T) {
	driver := &fakeProviderDriver{deleteErr: errors.New("provider delete failed")}
	dp := newDriverProvisioner(t, driver)

	err := dp.Destroy(context.Background(), model.Pool{Name: "web"}, model.Resource{ID: "provider-res-1"})
	if err == nil {
		t.Fatal("Destroy error = nil, want provider failure")
	}
	if len(driver.deleted) != 1 {
		t.Fatalf("deleted = %v, want one provider delete attempt", driver.deleted)
	}
}

func TestDriverProvisioner_AllocatePrefersGuestPersonalizer(t *testing.T) {
	driver := &fakeProviderDriver{personalize: true}
	dp := newDriverProvisioner(t, driver)

	props, err := dp.Allocate(context.Background(), model.Pool{Name: "web"}, model.Resource{ID: "provider-res-1"})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if props["ssh_host"] != "10.0.0.5" {
		t.Fatalf("props = %+v, want personalized access details", props)
	}
	if len(driver.personalized) != 1 || len(driver.allocated) != 0 {
		t.Fatalf("personalized=%v allocated=%v, want personalizer path only", driver.personalized, driver.allocated)
	}
}

func TestDriverProvisioner_AllocateFallsBackWhenPersonalizerHasNoResult(t *testing.T) {
	driver := &fakeProviderDriver{}
	dp := newDriverProvisioner(t, driver)

	props, err := dp.Allocate(context.Background(), model.Pool{Name: "web"}, model.Resource{ID: "provider-res-1"})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if props["allocated"] != "provider-res-1" {
		t.Fatalf("props = %+v, want fallback allocation properties", props)
	}
	if len(driver.personalized) != 1 || len(driver.allocated) != 1 {
		t.Fatalf("personalized=%v allocated=%v, want personalizer then fallback allocate", driver.personalized, driver.allocated)
	}
}

func TestDriverProvisioner_DestroyRejectsEmptyResourceID(t *testing.T) {
	driver := &fakeProviderDriver{}
	dp := newDriverProvisioner(t, driver)

	err := dp.Destroy(context.Background(), model.Pool{Name: "web"}, model.Resource{})
	if err == nil {
		t.Fatal("Destroy error = nil, want empty resource id error")
	}
	if len(driver.deleted) != 0 {
		t.Fatalf("deleted = %v, want no provider calls", driver.deleted)
	}
}
