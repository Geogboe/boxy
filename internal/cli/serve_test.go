package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/store"
)

type fakeServePoolReconciler struct {
	calls []model.PoolName
}

type servePoolReconcilerFunc func(ctx context.Context, poolName model.PoolName) error

func (f servePoolReconcilerFunc) Reconcile(ctx context.Context, poolName model.PoolName) error {
	return f(ctx, poolName)
}

type serveDriverConfig struct {
	Image string `json:"image"`
}

type serveDriver struct {
	providerType providersdk.Type
	cfg          any
}

func (d serveDriver) Type() providersdk.Type { return d.providerType }
func (d serveDriver) Create(context.Context, any) (*providersdk.Resource, error) {
	return &providersdk.Resource{}, nil
}
func (d serveDriver) Read(context.Context, string) (*providersdk.ResourceStatus, error) {
	return &providersdk.ResourceStatus{}, nil
}
func (d serveDriver) Update(context.Context, string, providersdk.Operation) (*providersdk.Result, error) {
	return &providersdk.Result{}, nil
}
func (d serveDriver) Delete(context.Context, string) error { return nil }
func (d serveDriver) Allocate(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (r *fakeServePoolReconciler) Reconcile(ctx context.Context, poolName model.PoolName) error {
	_ = ctx
	r.calls = append(r.calls, poolName)
	return nil
}

type fakeServeSandboxReconciler struct {
	calls int
}

func (r *fakeServeSandboxReconciler) Reconcile(ctx context.Context) error {
	_ = ctx
	r.calls++
	return nil
}

func TestServeReconcilePass_ReconcilesPoolsBeforeAndAfterSandboxFulfillment(t *testing.T) {
	t.Parallel()

	pools := &fakeServePoolReconciler{}
	sandboxes := &fakeServeSandboxReconciler{}

	serveReconcilePass(context.Background(), pools, nil, sandboxes, []model.PoolName{"web", "win"}, newServeUI(false))

	if sandboxes.calls != 1 {
		t.Fatalf("sandbox reconcile calls = %d, want 1", sandboxes.calls)
	}

	want := []model.PoolName{"web", "win", "web", "win"}
	if len(pools.calls) != len(want) {
		t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
	}
	for i := range want {
		if pools.calls[i] != want[i] {
			t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
		}
	}
}

func TestResolveServeOptionsPreferFlagsThenConfigDefaults(t *testing.T) {
	cfgUIFalse := false
	cfg := boxyconfig.Config{
		Server: boxyconfig.ServerSpec{Listen: ":7777", UI: &cfgUIFalse},
	}

	cmd := newServeCommand()
	if got := resolveListenAddr(serveOpts{}, cmd, cfg); got != ":7777" {
		t.Fatalf("resolveListenAddr config = %q, want :7777", got)
	}
	if got := resolveUIEnabled(serveOpts{}, cmd, cfg); got {
		t.Fatal("resolveUIEnabled config = true, want false")
	}

	cmd = newServeCommand()
	if err := cmd.Flags().Set("listen", ":8888"); err != nil {
		t.Fatalf("set listen: %v", err)
	}
	if err := cmd.Flags().Set("ui", "true"); err != nil {
		t.Fatalf("set ui: %v", err)
	}
	if got := resolveListenAddr(serveOpts{listen: ":8888"}, cmd, cfg); got != ":8888" {
		t.Fatalf("resolveListenAddr flag = %q, want :8888", got)
	}
	if got := resolveUIEnabled(serveOpts{ui: true}, cmd, cfg); !got {
		t.Fatal("resolveUIEnabled flag = false, want true")
	}

	cmd = newServeCommand()
	if got := resolveListenAddr(serveOpts{}, cmd, boxyconfig.Config{}); got != defaultListenAddr {
		t.Fatalf("resolveListenAddr default = %q, want %q", got, defaultListenAddr)
	}
	if got := resolveUIEnabled(serveOpts{}, cmd, boxyconfig.Config{}); !got {
		t.Fatal("resolveUIEnabled default = false, want true")
	}
}

func TestLoadConfigFindsDefaultConfigInEffectiveWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOXY_WORKING_DIR", dir)
	cfgPath := filepath.Join(dir, "boxy.yml")
	if err := os.WriteFile(cfgPath, []byte("providers: []\npools: []\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, usedPath, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if usedPath != cfgPath {
		t.Fatalf("usedPath = %q, want %q", usedPath, cfgPath)
	}
	if len(cfg.Providers) != 0 || len(cfg.Pools) != 0 {
		t.Fatalf("cfg = %+v, want empty config from default file", cfg)
	}
}

func TestLoadConfigReturnsDefaultsWhenNoConfigFileExists(t *testing.T) {
	t.Setenv("BOXY_WORKING_DIR", t.TempDir())

	cfg, usedPath, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if usedPath != "" {
		t.Fatalf("usedPath = %q, want empty", usedPath)
	}
	if len(cfg.Providers) != 0 || len(cfg.Pools) != 0 {
		t.Fatalf("cfg = %+v, want zero-value config", cfg)
	}
}

func TestDisplayAddr(t *testing.T) {
	tests := map[string]string{
		":9090":        "127.0.0.1:9090",
		"0.0.0.0:9090": "127.0.0.1:9090",
		"localhost:80": "localhost:80",
	}
	for input, want := range tests {
		if got := displayAddr(input); got != want {
			t.Fatalf("displayAddr(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildDriversDecodesConfiguredInstancesAndDefaults(t *testing.T) {
	reg := providersdk.NewRegistry()
	var configs []any
	for _, typ := range []providersdk.Type{"alpha", "beta"} {
		typ := typ
		if err := reg.Register(providersdk.Registration{
			Type:        typ,
			ConfigProto: func() any { return &serveDriverConfig{} },
			NewDriver: func(cfg any) (providersdk.Driver, error) {
				configs = append(configs, cfg)
				return serveDriver{providerType: typ, cfg: cfg}, nil
			},
		}); err != nil {
			t.Fatalf("register %q: %v", typ, err)
		}
	}

	drivers, err := buildDrivers(reg, []providersdk.Instance{
		{Name: "alpha-local", Type: "alpha", Config: map[string]any{"image": "alpine"}},
	})
	if err != nil {
		t.Fatalf("buildDrivers: %v", err)
	}
	if len(drivers) != 2 {
		t.Fatalf("drivers len = %d, want 2", len(drivers))
	}
	if types := providerTypes(reg); len(types) != 2 || types[0] != "alpha" || types[1] != "beta" {
		t.Fatalf("providerTypes = %v, want [alpha beta]", types)
	}
	if cfg, ok := configs[0].(*serveDriverConfig); !ok || cfg.Image != "alpine" {
		t.Fatalf("alpha config = %#v, want decoded image", configs[0])
	}
	if cfg, ok := configs[1].(*serveDriverConfig); !ok || cfg.Image != "" {
		t.Fatalf("beta config = %#v, want zero-value default", configs[1])
	}
}

func TestBuildDriversReportsDecodeAndFactoryErrors(t *testing.T) {
	reg := providersdk.NewRegistry()
	if err := reg.Register(providersdk.Registration{
		Type:        "alpha",
		ConfigProto: func() any { return &serveDriverConfig{} },
		NewDriver: func(any) (providersdk.Driver, error) {
			return nil, fmt.Errorf("factory failed")
		},
	}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}

	if _, err := buildDrivers(reg, []providersdk.Instance{{Name: "alpha-local", Type: "alpha", Config: map[string]any{"image": map[string]any{"bad": true}}}}); err == nil {
		t.Fatal("buildDrivers decode error = nil")
	}

	if _, err := buildDrivers(reg, nil); err == nil {
		t.Fatal("buildDrivers factory error = nil")
	}
}

func TestServeReconcilePass_RunsPostFulfillmentPoolReconcileEvenAfterSandboxError(t *testing.T) {
	t.Parallel()

	pools := &fakeServePoolReconciler{}
	sandboxes := serveSandboxReconcilerFunc(func(ctx context.Context) error {
		_ = ctx
		return fmt.Errorf("boom")
	})

	serveReconcilePass(context.Background(), pools, nil, sandboxes, []model.PoolName{"web"}, newServeUI(false))

	want := []model.PoolName{"web", "web"}
	if len(pools.calls) != len(want) {
		t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
	}
	for i := range want {
		if pools.calls[i] != want[i] {
			t.Fatalf("pool reconcile calls = %v, want %v", pools.calls, want)
		}
	}
}

func TestServeReconcilePass_DeletesSandboxesBeforePoolRefill(t *testing.T) {
	t.Parallel()

	var order []string
	pools := servePoolReconcilerFunc(func(ctx context.Context, poolName model.PoolName) error {
		_ = ctx
		order = append(order, "pool:"+string(poolName))
		return nil
	})
	deleter := serveSandboxReconcilerFunc(func(ctx context.Context) error {
		_ = ctx
		order = append(order, "delete")
		return nil
	})
	fulfiller := serveSandboxReconcilerFunc(func(ctx context.Context) error {
		_ = ctx
		order = append(order, "fulfill")
		return nil
	})

	serveReconcilePass(context.Background(), pools, deleter, fulfiller, []model.PoolName{"web"}, newServeUI(false))

	want := []string{"delete", "pool:web", "fulfill", "pool:web"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestOpenServeStore_PersistsStateAcrossReopen(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "boxy.yaml")

	first, statePath, err := openServeStore(cfgPath)
	if err != nil {
		t.Fatalf("openServeStore(first): %v", err)
	}
	if want := filepath.Join(filepath.Dir(cfgPath), ".boxy", "state.json"); statePath != want {
		t.Fatalf("state path = %q, want %q", statePath, want)
	}

	sb := model.Sandbox{
		ID:       "sb-1",
		Name:     "persisted",
		Status:   model.SandboxStatusPending,
		Requests: []model.ResourceRequest{{Type: model.ResourceTypeContainer, Profile: "web", Count: 1}},
	}
	if err := first.CreateSandbox(context.Background(), sb); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	second, statePath2, err := openServeStore(cfgPath)
	if err != nil {
		t.Fatalf("openServeStore(second): %v", err)
	}
	if statePath2 != statePath {
		t.Fatalf("second state path = %q, want %q", statePath2, statePath)
	}

	got, err := second.GetSandbox(context.Background(), sb.ID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if got.ID != sb.ID || got.Status != model.SandboxStatusPending {
		t.Fatalf("sandbox = %+v, want pending sandbox %q", got, sb.ID)
	}
}

func TestSeedConfiguredPools_PreservesInventoryAndUpdatesConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := store.NewMemoryStore()
	embedded := model.Resource{
		ID:         "res-ready",
		Type:       model.ResourceTypeVM,
		Profile:    "win-vm",
		OriginPool: "win-vm",
		State:      model.ResourceStateReady,
		Properties: map[string]any{"source": "embedded"},
	}

	global := embedded
	global.Properties = map[string]any{"source": "global"}

	if err := st.PutPool(ctx, model.Pool{
		Name: "win-vm",
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{MinReady: 1, MaxTotal: 1},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeVM,
			ExpectedProfile: "win-vm",
			Resources:       []model.Resource{embedded},
		},
	}); err != nil {
		t.Fatalf("put existing pool: %v", err)
	}
	if err := st.PutResource(ctx, global); err != nil {
		t.Fatalf("put resource: %v", err)
	}

	names, err := seedConfiguredPools(ctx, st, []boxyconfig.PoolSpec{{
		Name: "win-vm",
		Type: "vm",
		Policy: boxyconfig.PoolPolicySpec{
			Preheat: boxyconfig.PreheatPolicySpec{MinReady: 2, MaxTotal: 3},
		},
	}})
	if err != nil {
		t.Fatalf("seedConfiguredPools: %v", err)
	}
	if len(names) != 1 || names[0] != "win-vm" {
		t.Fatalf("names = %v, want [win-vm]", names)
	}

	got, err := st.GetPool(ctx, "win-vm")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if got.Policies.Preheat.MinReady != 2 || got.Policies.Preheat.MaxTotal != 3 {
		t.Fatalf("preheat policy = %+v, want min_ready=2 max_total=3", got.Policies.Preheat)
	}
	if len(got.Inventory.Resources) != 1 || got.Inventory.Resources[0].ID != "res-ready" {
		t.Fatalf("inventory resources = %+v, want res-ready", got.Inventory.Resources)
	}
	if got.Inventory.Resources[0].Properties["source"] != "global" {
		t.Fatalf("inventory resource source = %v, want global", got.Inventory.Resources[0].Properties["source"])
	}
}

func TestSeedConfiguredPools_PreservesOperatorDrainOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := store.NewMemoryStore()
	if err := st.PutPool(ctx, model.Pool{
		Name:  "web",
		Drain: model.PoolDrainState{Operator: true},
		Inventory: model.ResourceCollection{
			ExpectedType:    model.ResourceTypeContainer,
			ExpectedProfile: "web",
		},
	}); err != nil {
		t.Fatalf("put existing pool: %v", err)
	}

	if _, err := seedConfiguredPools(ctx, st, []boxyconfig.PoolSpec{{Name: "web", Type: "container"}}); err != nil {
		t.Fatalf("seedConfiguredPools: %v", err)
	}

	got, err := st.GetPool(ctx, "web")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if !got.Drain.Operator {
		t.Fatalf("operator drain override = false, want true")
	}
}

func TestSeedConfiguredPools_ReconstructsReadyInventoryFromResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := store.NewMemoryStore()
	resources := []model.Resource{
		{ID: "res-ready", Type: model.ResourceTypeVM, Profile: "win-vm", OriginPool: "win-vm", State: model.ResourceStateReady},
		{ID: "res-allocated", Type: model.ResourceTypeVM, Profile: "win-vm", OriginPool: "win-vm", State: model.ResourceStateAllocated},
		{ID: "res-destroyed", Type: model.ResourceTypeVM, Profile: "win-vm", OriginPool: "win-vm", State: model.ResourceStateDestroyed},
		{ID: "res-provisioning", Type: model.ResourceTypeVM, Profile: "win-vm", OriginPool: "win-vm", State: model.ResourceStateProvisioning},
		{ID: "res-released", Type: model.ResourceTypeVM, Profile: "win-vm", OriginPool: "win-vm", State: model.ResourceStateReleased},
		{ID: "res-wrong-profile", Type: model.ResourceTypeVM, Profile: "other", OriginPool: "win-vm", State: model.ResourceStateReady},
		{ID: "res-wrong-type", Type: model.ResourceTypeContainer, Profile: "win-vm", OriginPool: "win-vm", State: model.ResourceStateReady},
		{ID: "res-other-pool", Type: model.ResourceTypeVM, Profile: "win-vm", OriginPool: "other", State: model.ResourceStateReady},
	}
	for _, res := range resources {
		if err := st.PutResource(ctx, res); err != nil {
			t.Fatalf("put resource %q: %v", res.ID, err)
		}
	}

	if _, err := seedConfiguredPools(ctx, st, []boxyconfig.PoolSpec{{Name: "win-vm", Type: "vm"}}); err != nil {
		t.Fatalf("seedConfiguredPools: %v", err)
	}

	got, err := st.GetPool(ctx, "win-vm")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if len(got.Inventory.Resources) != 1 || got.Inventory.Resources[0].ID != "res-ready" {
		t.Fatalf("inventory resources = %+v, want only res-ready", got.Inventory.Resources)
	}
}

func TestPoolSpecToModel_invalid_pool_type(t *testing.T) {
	t.Parallel()

	_, err := poolSpecToModel(boxyconfig.PoolSpec{Name: "test", Type: "badtype"})
	if err == nil {
		t.Fatal("poolSpecToModel() error = nil, want invalid pool type")
	}
	if got, want := err.Error(), `pool "test" type invalid: unsupported pool type "badtype"`; got != want {
		t.Fatalf("poolSpecToModel() error = %q, want %q", got, want)
	}
}

func TestPoolSpecToModel_DrainExplicitnessFromConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
providers: []
pools:
  - name: lazy
    type: container
    policy:
      preheat:
        min_ready: 0
  - name: drained
    type: container
    policy:
      preheat:
        min_ready: 0
        max_total: 0
  - name: capped
    type: container
    policy:
      preheat:
        min_ready: 0
        max_total: 2
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, _, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	lazy, err := poolSpecToModel(cfg.Pools[0])
	if err != nil {
		t.Fatalf("poolSpecToModel(lazy): %v", err)
	}
	if lazy.Policies.Preheat.MaxTotal != 0 || lazy.EffectivelyDrained() {
		t.Fatalf("lazy pool max_total=%d drained=%t, want unbounded and not drained", lazy.Policies.Preheat.MaxTotal, lazy.EffectivelyDrained())
	}

	drained, err := poolSpecToModel(cfg.Pools[1])
	if err != nil {
		t.Fatalf("poolSpecToModel(drained): %v", err)
	}
	if !drained.Drain.ConfigDeclared || !drained.EffectivelyDrained() {
		t.Fatalf("drained pool drain state = %+v, want config-declared drain", drained.Drain)
	}

	capped, err := poolSpecToModel(cfg.Pools[2])
	if err != nil {
		t.Fatalf("poolSpecToModel(capped): %v", err)
	}
	if capped.Policies.Preheat.MaxTotal != 2 || capped.EffectivelyDrained() {
		t.Fatalf("capped pool max_total=%d drained=%t, want finite cap and not drained", capped.Policies.Preheat.MaxTotal, capped.EffectivelyDrained())
	}
}
