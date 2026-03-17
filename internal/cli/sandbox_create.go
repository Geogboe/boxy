package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	boxyconfig "github.com/Geogboe/boxy/v2/internal/config"
	"github.com/Geogboe/boxy/v2/pkg/model"
	"github.com/Geogboe/boxy/v2/internal/pool"
	"github.com/Geogboe/boxy/v2/internal/sandbox"
	"github.com/Geogboe/boxy/v2/pkg/store"
	"github.com/Geogboe/boxy/v2/pkg/providersdk"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/builtins"
)

func sandboxCreate(ctx context.Context, opts sandboxCreateOpts) error {
	spec, err := boxyconfig.LoadSandboxFile(opts.file)
	if err != nil {
		return err
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("sandbox spec name is required")
	}
	if len(spec.Resources) == 0 {
		return fmt.Errorf("sandbox spec resources is required")
	}

	cfgPath, err := resolveConfigPath(opts.configPath, opts.file)
	if err != nil {
		return err
	}
	if cfgPath == "" {
		return fmt.Errorf("no config file found (expected boxy.yaml next to %q or in cwd)", opts.file)
	}

	cfg, err := boxyconfig.LoadFile(cfgPath)
	if err != nil {
		return err
	}

	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return fmt.Errorf("register builtin providers: %w", err)
	}

	providers := ensureImplicitProviders(cfg.Providers, cfg.Pools)
	if err := reg.ValidateInstances(ctx, providers); err != nil {
		return fmt.Errorf("validate providers: %w", err)
	}

	statePath := opts.statePath
	if statePath == "" {
		statePath = filepath.Join(filepath.Dir(cfgPath), ".boxy", "state.json")
	}
	st, err := store.NewDiskStore(statePath)
	if err != nil {
		return err
	}

	specByName := make(map[model.PoolName]boxyconfig.PoolSpec, len(cfg.Pools))
	for i := range cfg.Pools {
		p := cfg.Pools[i]
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("pools[%d].name is required", i)
		}
		specByName[model.PoolName(p.Name)] = p
	}
	providerByName := make(map[string]providersdk.Instance, len(providers))
	for _, inst := range providers {
		providerByName[inst.Name] = inst
	}

	for _, ps := range cfg.Pools {
		mp, err := poolModelFromSpec(ps)
		if err != nil {
			return err
		}
		if err := upsertPool(ctx, st, mp); err != nil {
			return err
		}
	}

	needByPool := make(map[model.PoolName]int)
	for i := range spec.Resources {
		r := spec.Resources[i]
		if strings.TrimSpace(r.Pool) == "" {
			return fmt.Errorf("resources[%d].pool is required", i)
		}
		if r.Count <= 0 {
			return fmt.Errorf("resources[%d].count must be > 0", i)
		}
		needByPool[model.PoolName(r.Pool)] += r.Count
	}

	prov := &pool.DriverProvisioner{
		Registry:  reg,
		Specs:     specByName,
		Providers: providerByName,
	}
	pm := pool.New(st, prov)

	for poolName, need := range needByPool {
		p, err := st.GetPool(ctx, poolName)
		if err != nil {
			return fmt.Errorf("get pool %q: %w", poolName, err)
		}
		if p.Policies.Preheat.MinReady < need {
			p.Policies.Preheat.MinReady = need
			if err := st.PutPool(ctx, p); err != nil {
				return fmt.Errorf("put pool %q: %w", poolName, err)
			}
		}
		if err := pm.Reconcile(ctx, poolName); err != nil {
			return fmt.Errorf("reconcile pool %q: %w", poolName, err)
		}
	}

	sm := sandbox.New(st)
	sb, err := sm.Create(ctx, spec.Name, model.SandboxPolicies{})
	if err != nil {
		return err
	}

	for _, req := range spec.Resources {
		sb, err = sm.AddFromPool(ctx, sb.ID, model.PoolName(req.Pool), req.Count)
		if err != nil {
			return err
		}
	}

	// Replenish pools after allocation so preheat targets stay satisfied.
	for poolName := range needByPool {
		if err := pm.Reconcile(ctx, poolName); err != nil {
			return fmt.Errorf("reconcile pool %q after allocation: %w", poolName, err)
		}
	}

	slog.Info("sandbox created", "id", sb.ID, "name", sb.Name, "resources", len(sb.Resources), "state", statePath)
	return nil
}

func upsertPool(ctx context.Context, st store.Store, desired model.Pool) error {
	existing, err := st.GetPool(ctx, desired.Name)
	if err != nil && err != store.ErrNotFound {
		return fmt.Errorf("get pool %q: %w", desired.Name, err)
	}
	if err == nil {
		// Preserve existing inventory to avoid orphaning resources on disk.
		if existing.Inventory.ExpectedType == desired.Inventory.ExpectedType && existing.Inventory.ExpectedProfile == desired.Inventory.ExpectedProfile {
			desired.Inventory.Resources = existing.Inventory.Resources
		}
	}
	if err := st.PutPool(ctx, desired); err != nil {
		return fmt.Errorf("put pool %q: %w", desired.Name, err)
	}
	return nil
}

func resolveConfigPath(explicitPath, sandboxFile string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if sandboxFile != "" {
		dir := filepath.Dir(sandboxFile)
		p, err := findConfigPathInDir(dir)
		if err != nil {
			return "", err
		}
		if p != "" {
			return p, nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return findConfigPathInDir(cwd)
}

func poolModelFromSpec(ps boxyconfig.PoolSpec) (model.Pool, error) {
	name := model.PoolName(strings.TrimSpace(ps.Name))
	if name == "" {
		return model.Pool{}, fmt.Errorf("pool name is required")
	}
	expectedType, err := poolExpectedType(ps.Type)
	if err != nil {
		return model.Pool{}, fmt.Errorf("pool %q type invalid: %w", name, err)
	}
	pol := ps.EffectivePolicy()
	return model.Pool{
		Name: name,
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{
				MinReady: pol.Preheat.MinReady,
				MaxTotal: pol.Preheat.MaxTotal,
			},
			Recycle: model.RecyclePolicy{
				MaxAge: pol.Recycle.MaxAge,
			},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    expectedType,
			ExpectedProfile: model.ResourceProfile(name),
			Resources:       nil,
		},
	}, nil
}

func poolExpectedType(t string) (model.ResourceType, error) {
	switch strings.TrimSpace(t) {
	case "container", "docker":
		return model.ResourceTypeContainer, nil
	case "vm":
		return model.ResourceTypeVM, nil
	case "share":
		return model.ResourceTypeShare, nil
	case "":
		return model.ResourceTypeContainer, nil
	default:
		return model.ResourceTypeUnknown, fmt.Errorf("unsupported pool type %q", t)
	}
}

func ensureImplicitProviders(explicit []providersdk.Instance, pools []boxyconfig.PoolSpec) []providersdk.Instance {
	out := make([]providersdk.Instance, 0, len(explicit)+2)
	out = append(out, explicit...)

	byName := make(map[string]providersdk.Instance, len(out))
	for _, inst := range out {
		byName[inst.Name] = inst
	}

	need := make(map[string]providersdk.Type)
	for _, p := range pools {
		name := strings.TrimSpace(p.Provider)
		if name == "" && strings.TrimSpace(p.Type) == "docker" {
			name = "docker-local"
		}
		if name == "" {
			// Default provider for container pools.
			name = "docker-local"
		}
		if _, exists := byName[name]; exists {
			continue
		}
		need[name] = "docker"
	}

	for name, typ := range need {
		if typ != "docker" {
			continue
		}
		host := "unix:///var/run/docker.sock"
		if name == "docker-remote" {
			// Placeholder that satisfies schema validation; override later via explicit config.
			host = "tcp://docker-host:2376"
		}
		out = append(out, providersdk.Instance{
			Name: name,
			Type: "docker",
			Config: map[string]any{
				"host": host,
			},
		})
	}

	return out
}
