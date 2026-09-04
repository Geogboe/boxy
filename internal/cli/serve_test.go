package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/pki"
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

	serveReconcilePass(context.Background(), pools, nil, sandboxes, nil, []model.PoolName{"web", "win"}, newServeUI(false))

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

func TestResolveGRPCCertSANs(t *testing.T) {
	cfg := boxyconfig.Config{
		Server: boxyconfig.ServerSpec{GRPCCertSANs: []string{"cfg-a.example.test", "cfg-b.example.test"}},
	}

	cmd := newServeCommand()
	got := resolveGRPCCertSANs(serveOpts{}, cmd, cfg)
	want := []string{"cfg-a.example.test", "cfg-b.example.test"}
	if !slices.Equal(got, want) {
		t.Fatalf("resolveGRPCCertSANs config = %v, want %v", got, want)
	}

	cmd = newServeCommand()
	if err := cmd.Flags().Set("grpc-cert-san", "flag-a.example.test"); err != nil {
		t.Fatalf("set grpc-cert-san: %v", err)
	}
	if err := cmd.Flags().Set("grpc-cert-san", "flag-b.example.test"); err != nil {
		t.Fatalf("set grpc-cert-san: %v", err)
	}
	got = resolveGRPCCertSANs(serveOpts{grpcCertSANs: []string{"flag-a.example.test", "flag-b.example.test"}}, cmd, cfg)
	want = []string{"flag-a.example.test", "flag-b.example.test"}
	if !slices.Equal(got, want) {
		t.Fatalf("resolveGRPCCertSANs flag = %v, want %v (should fully replace config, not merge)", got, want)
	}

	cmd = newServeCommand()
	if got := resolveGRPCCertSANs(serveOpts{}, cmd, boxyconfig.Config{}); got != nil {
		t.Fatalf("resolveGRPCCertSANs default = %v, want nil", got)
	}
}

func TestAgentCertSANs(t *testing.T) {
	cases := []struct {
		name       string
		listenAddr string
		extra      []string
		want       []string
	}{
		{
			name:       "wildcard_listen_no_extra",
			listenAddr: ":9091",
			want:       []string{"localhost", "127.0.0.1"},
		},
		{
			name:       "explicit_wildcard_host_excluded",
			listenAddr: "0.0.0.0:9091",
			want:       []string{"localhost", "127.0.0.1"},
		},
		{
			name:       "literal_host_included",
			listenAddr: "agent.example.test:9091",
			want:       []string{"localhost", "127.0.0.1", "agent.example.test"},
		},
		{
			name:       "extra_trimmed_deduped_blanks_dropped",
			listenAddr: ":9091",
			extra:      []string{"foo.example.test", "  ", "", "foo.example.test"},
			want:       []string{"localhost", "127.0.0.1", "foo.example.test"},
		},
		{
			name:       "extra_duplicating_auto_derived_entry_not_repeated",
			listenAddr: ":9091",
			extra:      []string{"localhost"},
			want:       []string{"localhost", "127.0.0.1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agentCertSANs(tc.listenAddr, tc.extra)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("agentCertSANs(%q, %v) = %v, want %v", tc.listenAddr, tc.extra, got, tc.want)
			}
		})
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

func TestBuildClientCAPool_RejectsInvalidPEM(t *testing.T) {
	_, err := buildClientCAPool([]byte("not a valid certificate"))
	if err == nil {
		t.Fatal("expected an error for a CA cert PEM with no valid certificates")
	}
}

func TestBuildClientCAPool_AcceptsValidPEM(t *testing.T) {
	ca, err := pki.EnsureCA(t.TempDir())
	if err != nil {
		t.Fatalf("ensure CA: %v", err)
	}

	pool, err := buildClientCAPool(ca.CertPEM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pool == nil {
		t.Fatal("expected a non-nil cert pool for a valid CA cert")
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
	}, "")
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

func TestEmbeddedProviderTypesExcludeRemotePinnedPools(t *testing.T) {
	providers := map[string]providersdk.Instance{
		"docker-local": {Name: "docker-local", Type: "docker"},
		"hyperv-local": {Name: "hyperv-local", Type: "hyperv"},
	}
	specs := []boxyconfig.PoolSpec{
		{Name: "docker", Type: "container", Provider: "docker-local"},
		{Name: "windows", Type: "vm", Provider: "hyperv-local", Agent: "remote-windows"},
	}
	got := embeddedProviderTypes(specs, providers, []providersdk.Type{"docker", "hyperv", "devfactory"})
	if len(got) != 1 || got[0] != "docker" {
		t.Fatalf("embedded provider types = %v, want [docker]", got)
	}
}

type resolvingServeDriverConfig struct {
	resolvedBaseDir string
}

func (c *resolvingServeDriverConfig) ResolveRelativePaths(baseDir string) {
	c.resolvedBaseDir = baseDir
}

func TestBuildDriversPassesCfgPathDirToRelativePathResolver(t *testing.T) {
	reg := providersdk.NewRegistry()
	var gotBaseDir string
	if err := reg.Register(providersdk.Registration{
		Type:        "alpha",
		ConfigProto: func() any { return &resolvingServeDriverConfig{} },
		NewDriver: func(cfg any) (providersdk.Driver, error) {
			gotBaseDir = cfg.(*resolvingServeDriverConfig).resolvedBaseDir
			return serveDriver{providerType: "alpha", cfg: cfg}, nil
		},
	}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}

	cfgPath := filepath.Join("some", "dir", "boxy.yaml")
	if _, err := buildDrivers(reg, nil, cfgPath); err != nil {
		t.Fatalf("buildDrivers: %v", err)
	}
	if want := filepath.Dir(cfgPath); gotBaseDir != want {
		t.Fatalf("ResolveRelativePaths baseDir = %q, want %q", gotBaseDir, want)
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

	if _, err := buildDrivers(reg, []providersdk.Instance{{Name: "alpha-local", Type: "alpha", Config: map[string]any{"image": map[string]any{"bad": true}}}}, ""); err == nil {
		t.Fatal("buildDrivers decode error = nil")
	}

	if _, err := buildDrivers(reg, nil, ""); err == nil {
		t.Fatal("buildDrivers factory error = nil")
	}
}

func TestBuildDriversRejectsDuplicateConfiguredProviderTypes(t *testing.T) {
	reg := providersdk.NewRegistry()
	if err := reg.Register(providersdk.Registration{
		Type:        "alpha",
		ConfigProto: func() any { return &serveDriverConfig{} },
		NewDriver:   func(any) (providersdk.Driver, error) { return serveDriver{providerType: "alpha"}, nil },
	}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}

	_, err := buildDrivers(reg, []providersdk.Instance{
		{Name: "alpha-a", Type: "alpha"},
		{Name: "alpha-b", Type: "alpha"},
	}, "")
	if err == nil {
		t.Fatal("buildDrivers duplicate provider type error = nil")
	}
	if !strings.Contains(err.Error(), `provider type "alpha" has multiple configured instances`) {
		t.Fatalf("buildDrivers error = %q, want duplicate provider type message", err)
	}
}

func TestServeReconcilePass_RunsPostFulfillmentPoolReconcileEvenAfterSandboxError(t *testing.T) {
	t.Parallel()

	pools := &fakeServePoolReconciler{}
	sandboxes := serveSandboxReconcilerFunc(func(ctx context.Context) error {
		_ = ctx
		return fmt.Errorf("boom")
	})

	serveReconcilePass(context.Background(), pools, nil, sandboxes, nil, []model.PoolName{"web"}, newServeUI(false))

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

	serveReconcilePass(context.Background(), pools, deleter, fulfiller, nil, []model.PoolName{"web"}, newServeUI(false))

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

func TestServeReconcilePass_RunsSessionSweeper(t *testing.T) {
	t.Parallel()

	pools := &fakeServePoolReconciler{}
	sweptCalls := 0
	sweeper := serveSandboxReconcilerFunc(func(ctx context.Context) error {
		_ = ctx
		sweptCalls++
		return nil
	})

	serveReconcilePass(context.Background(), pools, nil, nil, sweeper, []model.PoolName{"web"}, newServeUI(false))

	if sweptCalls != 1 {
		t.Fatalf("session sweeper calls = %d, want 1", sweptCalls)
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

func TestResolveServeOpts_ServiceConfig_LoadsFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "service.yaml")

	if err := saveServeServiceConfig(cfgPath, serveServiceConfig{
		Listen:     ":19090",
		UI:         false,
		GRPCListen: ":19091",
		LogFile:    filepath.Join(dir, "service.log"),
	}); err != nil {
		t.Fatalf("saveServeServiceConfig: %v", err)
	}

	opts, err := resolveServeOpts(serveOpts{serviceConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("resolveServeOpts: %v", err)
	}
	if opts.listen != ":19090" || opts.ui != false || opts.grpcListen != ":19091" {
		t.Fatalf("resolved opts = %+v, unexpected", opts)
	}
}

func TestResolveServeOpts_ServiceConfig_WinsOverBoxyYAMLConfig(t *testing.T) {
	// Regression for the gap the ledger flagged in Task 12: resolveServeOpts
	// alone returning the right value proves nothing if the downstream
	// resolveListenAddr/resolveUIEnabled/resolveGRPCListenAddr/
	// resolveGRPCCertSANs still discard it because --listen/--ui/etc were
	// never Set() on cmd (only --service-config was). Give boxy.yaml a
	// deliberately different value so a silent fall-through to it is
	// caught instead of passing vacuously.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "service.yaml")
	if err := saveServeServiceConfig(cfgPath, serveServiceConfig{
		Listen:       ":19090",
		UI:           false,
		GRPCListen:   ":19091",
		GRPCCertSANs: []string{"svc-cfg.example.test"},
		LogFile:      filepath.Join(dir, "service.log"),
	}); err != nil {
		t.Fatalf("saveServeServiceConfig: %v", err)
	}

	opts, err := resolveServeOpts(serveOpts{serviceConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("resolveServeOpts: %v", err)
	}

	cfg := boxyconfig.Config{
		Server: boxyconfig.ServerSpec{Listen: ":8080", GRPCListen: ":8081", GRPCCertSANs: []string{"boxy-yaml.example.test"}},
	}
	cmd := newServeCommand() // --listen/--ui/--grpc-listen/--grpc-cert-san never Set()

	if got := resolveListenAddr(opts, cmd, cfg); got != ":19090" {
		t.Fatalf("resolveListenAddr = %q, want service-config value :19090 (not boxy.yaml's :8080)", got)
	}
	if got := resolveUIEnabled(opts, cmd, cfg); got {
		t.Fatal("resolveUIEnabled = true, want service-config value false")
	}
	if got := resolveGRPCListenAddr(opts, cmd, cfg); got != ":19091" {
		t.Fatalf("resolveGRPCListenAddr = %q, want service-config value :19091 (not boxy.yaml's :8081)", got)
	}
	if got := resolveGRPCCertSANs(opts, cmd, cfg); !slices.Equal(got, []string{"svc-cfg.example.test"}) {
		t.Fatalf("resolveGRPCCertSANs = %v, want service-config value", got)
	}
}

func TestResolveServeOpts_ServiceConfig_EmptyListenFieldsFallBackToDefaults(t *testing.T) {
	// `serve service install` persists the raw --listen/--grpc-listen flag
	// value, which is "" when the operator didn't pass one — resolveServeOpts
	// must apply the same defaults resolveListenAddr/resolveGRPCListenAddr
	// would, not persist/return an empty bind address.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "service.yaml")
	if err := saveServeServiceConfig(cfgPath, serveServiceConfig{LogFile: filepath.Join(dir, "service.log")}); err != nil {
		t.Fatalf("saveServeServiceConfig: %v", err)
	}

	opts, err := resolveServeOpts(serveOpts{serviceConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("resolveServeOpts: %v", err)
	}
	if opts.listen != defaultListenAddr {
		t.Fatalf("opts.listen = %q, want default %q", opts.listen, defaultListenAddr)
	}
	if opts.grpcListen != defaultGRPCListenAddr {
		t.Fatalf("opts.grpcListen = %q, want default %q", opts.grpcListen, defaultGRPCListenAddr)
	}
}

func TestResolveServeOpts_NoServiceConfig_ReturnsOptsUnchanged(t *testing.T) {
	given := serveOpts{listen: ":9090", ui: true}
	got, err := resolveServeOpts(given)
	if err != nil {
		t.Fatalf("resolveServeOpts: %v", err)
	}
	// serveOpts has a []string field (grpcCertSANs), so it isn't
	// comparable with == / != — use reflect.DeepEqual instead (add
	// "reflect" to this file's imports if serve_test.go doesn't already
	// have it).
	if !reflect.DeepEqual(got, given) {
		t.Fatalf("resolveServeOpts(%+v) = %+v, want unchanged", given, got)
	}
}
