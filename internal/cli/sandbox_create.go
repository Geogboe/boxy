package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/pterm/pterm"
	"golang.org/x/term"
)

func sandboxCreate(ctx context.Context, opts sandboxCreateOpts) error {
	// Silence internal slog output; progress is reported via pterm spinners.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	pterm.Println()

	// --- Load sandbox spec ---
	doneSpec := step("Loading sandbox spec")
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
	doneSpec(fmt.Sprintf("%s  (%d resource group(s))", spec.Name, len(spec.Resources)))

	// --- Load config ---
	doneConfig := step("Loading config")
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
	doneConfig(fmt.Sprintf("%s  (%d provider(s), %d pool(s))", filepath.Base(cfgPath), len(cfg.Providers), len(cfg.Pools)))

	// --- Register + validate providers ---
	doneProviders := step("Registering providers")
	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return fmt.Errorf("register builtin providers: %w", err)
	}
	providers := ensureImplicitProviders(cfg.Providers, cfg.Pools)
	if err := reg.ValidateInstances(ctx, providers); err != nil {
		return fmt.Errorf("validate providers: %w", err)
	}
	doneProviders(strings.Join(providerTypes(reg), ", "))

	// --- Open state store ---
	doneState := step("Opening state")
	statePath := opts.statePath
	if statePath == "" {
		statePath = filepath.Join(filepath.Dir(cfgPath), ".boxy", "state.json")
	}
	st, err := store.NewDiskStore(statePath)
	if err != nil {
		return err
	}
	doneState(statePath)

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

	pterm.Println()
	pterm.Bold.Printfln("  Creating sandbox %q", spec.Name)
	pterm.Println()

	drivers, err := buildDrivers(reg, providers)
	if err != nil {
		return fmt.Errorf("build drivers: %w", err)
	}
	embeddedAgent, err := agentsdk.NewEmbeddedAgent("embedded", "Embedded Agent", drivers...)
	if err != nil {
		return fmt.Errorf("create embedded agent: %w", err)
	}

	prov := &pool.AgentProvisioner{
		Agent:     embeddedAgent,
		Specs:     specByName,
		Providers: providerByName,
	}
	pm := pool.New(st, prov)
	sm := sandbox.New(st, prov)

	// --- Ensure pools have enough ready resources ---
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
		label := fmt.Sprintf("Pool %s  provisioning %d resource(s)", poolName, need)
		spin, _ := boxySpinner.Start(label)
		t0 := time.Now()
		if err := pm.Reconcile(ctx, poolName); err != nil {
			spin.Fail(err.Error())
			return fmt.Errorf("reconcile pool %q: %w", poolName, err)
		}
		spin.Success(label + "  " + pterm.FgDarkGray.Sprintf("done  (%s)", time.Since(t0).Round(time.Millisecond)))
	}

	// --- Allocate resources into sandbox ---
	doneAllocate := step(fmt.Sprintf("Allocating sandbox %q", spec.Name))
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
	doneAllocate(fmt.Sprintf("%s  ·  %d resource(s)", sb.ID, len(sb.Resources)))

	// Collect full resource details (includes Allocate-time properties).
	resources := make([]model.Resource, 0, len(sb.Resources))
	for _, rid := range sb.Resources {
		res, err := st.GetResource(ctx, rid)
		if err == nil {
			resources = append(resources, res)
		}
	}

	// --- Replenish pools so preheat targets stay satisfied ---
	for poolName := range needByPool {
		label := fmt.Sprintf("Pool %s  replenishing", poolName)
		spin, _ := boxySpinner.Start(label)
		t0 := time.Now()
		if err := pm.Reconcile(ctx, poolName); err != nil {
			spin.Fail(err.Error())
			return fmt.Errorf("reconcile pool %q after allocation: %w", poolName, err)
		}
		spin.Success(label + "  " + pterm.FgDarkGray.Sprintf("done  (%s)", time.Since(t0).Round(time.Millisecond)))
	}

	printConnectionInfo(sb, resources)

	if !opts.noEnvFile && term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // Fd() fits in int on all supported platforms
		if err := promptEnvFile(sb, resources); err != nil {
			return err
		}
	}

	pterm.FgDarkGray.Printfln("  boxy sandbox get %s", sb.ID)
	pterm.FgDarkGray.Printfln("  boxy sandbox delete %s", sb.ID)
	pterm.Println()

	return nil
}

// printConnectionInfo displays sandbox connection info once to the terminal.
func printConnectionInfo(sb model.Sandbox, resources []model.Resource) {
	if len(resources) == 0 {
		return
	}
	lines := buildEnvLines(sb, resources)

	pterm.Println()
	pterm.Bold.Println("  Connection info  (save this — you won't see it again)")
	pterm.Println()
	for _, l := range lines {
		pterm.Printfln("    %s", l)
	}
	pterm.Println()
}

// promptEnvFile asks the user whether to save connection info as an env file.
func promptEnvFile(sb model.Sandbox, resources []model.Resource) error {
	envFileName := fmt.Sprintf(".sandbox-%s.env", sb.Name)
	pterm.Printfln("  [e] Save as %s   [enter] Skip", envFileName)
	pterm.Println()

	key, err := readSingleKey()
	if err != nil {
		return nil // non-fatal if we can't read the key
	}

	if key == 'e' || key == 'E' {
		return writeEnvFile(sb, resources, envFileName)
	}
	return nil
}

// writeEnvFile writes sandbox connection info to .sandbox-<name>.env in cwd.
func writeEnvFile(sb model.Sandbox, resources []model.Resource, filename string) error {
	wd, err := effectiveWD()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	path := filepath.Join(wd, filename)

	lines := buildEnvLines(sb, resources)
	var buf strings.Builder
	fmt.Fprintf(&buf, "# Boxy sandbox: %s (%s)\n", sb.Name, sb.ID)
	buf.WriteString("# Add this file to .gitignore — it may contain credentials.\n")
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(buf.String()), 0600); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}

	pterm.Println()
	pterm.Printfln("  Wrote %s", path)
	pterm.Warning.Println("  Add this file to .gitignore — it may contain credentials.")
	pterm.Println()
	return nil
}

// buildEnvLines builds the list of KEY=VALUE lines for the env file / display.
func buildEnvLines(sb model.Sandbox, resources []model.Resource) []string {
	var lines []string
	lines = append(lines, fmt.Sprintf("SANDBOX_ID=%s", sb.ID))
	lines = append(lines, "")

	seenPools := make(map[string]int) // pool -> count so far
	poolOrder := make([]string, 0)

	for _, res := range resources {
		poolName := string(res.Profile)
		if _, seen := seenPools[poolName]; !seen {
			seenPools[poolName] = 0
			poolOrder = append(poolOrder, poolName)
		}
	}
	// Maintain order: rebuild groups indexed by pool name.
	poolIdx := make(map[string]int, len(poolOrder))
	for i, name := range poolOrder {
		poolIdx[name] = i
	}
	groupSlice := make([][]model.Resource, len(poolOrder))
	for _, res := range resources {
		poolName := string(res.Profile)
		i := poolIdx[poolName]
		groupSlice[i] = append(groupSlice[i], res)
	}

	for pi, poolName := range poolOrder {
		prefix := "SANDBOX_" + envVarSegment(poolName)
		for n, res := range groupSlice[pi] {
			varPrefix := fmt.Sprintf("%s_%d", prefix, n+1)

			// Collect all property keys and sort for deterministic output.
			keys := make([]string, 0, len(res.Properties))
			for k := range res.Properties {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				v := res.Properties[k]
				envKey := varPrefix + "_" + envVarSegment(k)
				lines = append(lines, fmt.Sprintf("%s=%v", envKey, v))
			}
			if pi < len(poolOrder)-1 || n < len(groupSlice[pi])-1 {
				lines = append(lines, "")
			}
		}
	}
	return lines
}

// envVarSegment converts a string to an uppercase env var segment
// (hyphens and dots become underscores).
func envVarSegment(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	return s
}

// readSingleKey reads a single keypress without requiring Enter.
// Returns 0 and nil on non-interactive stdin.
func readSingleKey() (byte, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // Fd() fits in int on all supported platforms
		return 0, nil
	}
	old, err := term.MakeRaw(int(os.Stdin.Fd())) //nolint:gosec // Fd() fits in int on all supported platforms
	if err != nil {
		return 0, nil
	}
	defer term.Restore(int(os.Stdin.Fd()), old) //nolint:errcheck,gosec // Fd() fits in int on all supported platforms

	buf := make([]byte, 1)
	if _, err := os.Stdin.Read(buf); err != nil {
		return 0, nil
	}
	return buf[0], nil
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
	wd, err := effectiveWD()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return findConfigPathInDir(wd)
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
