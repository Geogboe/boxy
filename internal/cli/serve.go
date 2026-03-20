package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

const defaultListenAddr = ":9090"

type serveOpts struct {
	configPath string
	listen     string
	ui         bool
}

func newServeCommand() *cobra.Command {
	var opts serveOpts

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Boxy daemon (API server + reconcile loop)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), opts, cmd)
		},
	}

	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	cmd.Flags().StringVar(&opts.listen, "listen", "", "HTTP listen address (default :9090)")
	cmd.Flags().BoolVar(&opts.ui, "ui", true, "enable web dashboard UI")

	return cmd
}

func runServe(ctx context.Context, opts serveOpts, cmd *cobra.Command) error {
	logFile, _ := cmd.Root().PersistentFlags().GetString("log-file")
	ui := newServeUI(logFile == "")
	if logFile == "" {
		// Silence slog — pterm handles all user-facing output in this mode.
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	}

	// Config
	doneConfig := ui.step("Loading config")
	cfg, cfgPath, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	var configMsg string
	if cfgPath == "" {
		configMsg = "no config file (defaults)"
	} else {
		configMsg = fmt.Sprintf("%s (%d providers, %d pools)", filepath.Base(cfgPath), len(cfg.Providers), len(cfg.Pools))
	}
	doneConfig(configMsg)

	// Providers
	doneProviders := ui.step("Registering providers")
	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return fmt.Errorf("register builtin providers: %w", err)
	}
	doneProviders(strings.Join(providerTypes(reg), ", "))

	// Validate
	doneValidate := ui.step("Validating provider config")
	if err := reg.ValidateInstances(ctx, cfg.Providers); err != nil {
		return fmt.Errorf("validate providers: %w", err)
	}
	doneValidate(fmt.Sprintf("%d configured", len(cfg.Providers)))

	// Build lookup maps for the DriverProvisioner.
	specsMap := make(map[model.PoolName]boxyconfig.PoolSpec, len(cfg.Pools))
	for _, spec := range cfg.Pools {
		specsMap[model.PoolName(spec.Name)] = spec
	}
	providersMap := make(map[string]providersdk.Instance, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providersMap[p.Name] = p
	}

	st := store.NewMemoryStore()

	// Drivers + embedded agent
	doneAgent := ui.step("Starting embedded agent")
	drivers, err := buildDrivers(reg, cfg.Providers)
	if err != nil {
		return fmt.Errorf("build drivers: %w", err)
	}
	embeddedAgent, err := agentsdk.NewEmbeddedAgent("embedded", "Embedded Agent", drivers...)
	if err != nil {
		return fmt.Errorf("create embedded agent: %w", err)
	}
	doneAgent(fmt.Sprintf("%d drivers", len(drivers)))

	// Use AgentProvisioner to route pool operations through the agent.
	provisioner := &pool.AgentProvisioner{
		Agent:     embeddedAgent,
		Specs:     specsMap,
		Providers: providersMap,
	}
	poolMgr := pool.New(st, provisioner)
	sandboxMgr := sandbox.New(st, provisioner)

	// Pools
	donePools := ui.step("Initializing pools")
	poolNames := make([]model.PoolName, 0, len(cfg.Pools))
	for _, spec := range cfg.Pools {
		p, err := poolSpecToModel(spec)
		if err != nil {
			return fmt.Errorf("create pool model for %q: %w", spec.Name, err)
		}
		if err := st.PutPool(ctx, p); err != nil {
			return fmt.Errorf("seed pool %q: %w", spec.Name, err)
		}
		poolNames = append(poolNames, p.Name)
	}
	donePools(fmt.Sprintf("%d pool(s)", len(poolNames)))

	// Resolve listen address: flag > config > default
	listenAddr := resolveListenAddr(opts, cmd, cfg)

	// Resolve UI enabled: flag > config > default (true)
	uiEnabled := resolveUIEnabled(opts, cmd, cfg)

	srv := server.New(st, sandboxMgr, listenAddr, uiEnabled)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return srv.Start(ctx)
	})
	g.Go(func() error {
		return serveLoop(ctx, poolMgr, poolNames, ui)
	})

	printServeBanner(listenAddr, uiEnabled, len(cfg.Pools))

	return g.Wait()
}

// resolveListenAddr picks the listen address with precedence:
// explicit --listen flag > config server.listen > default :9090
func resolveListenAddr(opts serveOpts, cmd *cobra.Command, cfg boxyconfig.Config) string {
	if cmd.Flags().Changed("listen") {
		return opts.listen
	}
	if cfg.Server.Listen != "" {
		return cfg.Server.Listen
	}
	return defaultListenAddr
}

// resolveUIEnabled picks the UI toggle with precedence:
// explicit --ui flag > config server.ui > default true
func resolveUIEnabled(opts serveOpts, cmd *cobra.Command, cfg boxyconfig.Config) bool {
	if cmd.Flags().Changed("ui") {
		return opts.ui
	}
	return cfg.Server.UIEnabled()
}

func loadConfig(explicitPath string) (cfg boxyconfig.Config, usedPath string, _ error) {
	if explicitPath != "" {
		c, err := boxyconfig.LoadFile(explicitPath)
		if err != nil {
			return boxyconfig.Config{}, "", err
		}
		return c, explicitPath, nil
	}

	path, err := findDefaultConfigPath()
	if err != nil {
		return boxyconfig.Config{}, "", err
	}
	if path == "" {
		return boxyconfig.Config{}, "", nil
	}

	c, err := boxyconfig.LoadFile(path)
	if err != nil {
		return boxyconfig.Config{}, "", err
	}
	return c, path, nil
}

func findDefaultConfigPath() (string, error) {
	wd, err := effectiveWD()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return findConfigPathInDir(wd)
}

func serveLoop(ctx context.Context, poolMgr *pool.Manager, poolNames []model.PoolName, ui *serveUI) error {
	const tickEvery = 10 * time.Second

	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ui.shutdown()
			return nil
		case <-ticker.C:
			for _, name := range poolNames {
				if err := poolMgr.Reconcile(ctx, name); err != nil {
					ui.reconcileError(name, err)
				}
			}
		}
	}
}

// poolSpecToModel converts a config PoolSpec into a runtime model.Pool.
// Initializes the pool's inventory with the expected type and profile.
func poolSpecToModel(spec boxyconfig.PoolSpec) (model.Pool, error) {
	expectedType, err := poolExpectedType(spec.Type)
	if err != nil {
		return model.Pool{}, fmt.Errorf("pool %q type invalid: %w", spec.Name, err)
	}
	policy := spec.EffectivePolicy()
	return model.Pool{
		Name: model.PoolName(spec.Name),
		Policies: model.PoolPolicies{
			Preheat: model.PreheatPolicy{
				MinReady: policy.Preheat.MinReady,
				MaxTotal: policy.Preheat.MaxTotal,
			},
			Recycle: model.RecyclePolicy{
				MaxAge: policy.Recycle.MaxAge,
			},
		},
		Inventory: model.ResourceCollection{
			ExpectedType:    expectedType,
			ExpectedProfile: model.ResourceProfile(spec.Name),
		},
	}, nil
}

// printServeBanner writes the startup banner to the terminal via pterm.
func printServeBanner(listenAddr string, uiEnabled bool, poolCount int) {
	host := displayAddr(listenAddr)

	pterm.Println()
	pterm.Bold.Printfln("  Boxy is running")
	pterm.Println()
	if uiEnabled {
		pterm.Printfln("    Dashboard   http://%s/", host)
	}
	pterm.Printfln("    API         http://%s/api/v1/", host)
	pterm.Printfln("    Health      http://%s/healthz", host)
	pterm.Println()
	pterm.Printfln("  Pools: %d configured  ·  Press Ctrl+C to stop", poolCount)
	pterm.Println()
}

// displayAddr resolves a listen address for display.
// ":9090" becomes "127.0.0.1:9090"; "0.0.0.0:9090" becomes "127.0.0.1:9090".
func displayAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "127.0.0.1" + addr
	}
	if len(addr) > 7 && addr[:7] == "0.0.0.0" {
		return "127.0.0.1" + addr[7:]
	}
	return addr
}

func providerTypes(reg *providersdk.Registry) []string {
	ts := reg.Types()
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, string(t))
	}
	return out
}

// buildDrivers instantiates drivers for all registered provider types.
// For each type in the registry:
// - If a provider instance with matching Type exists, use its Config
// - Otherwise, use the zero-value config (defaults)
func buildDrivers(reg *providersdk.Registry, instances []providersdk.Instance) ([]providersdk.Driver, error) {
	types := reg.Types()
	drivers := make([]providersdk.Driver, 0, len(types))

	// Build a map of type -> instance config for easy lookup.
	configByType := make(map[providersdk.Type]map[string]any)
	for _, inst := range instances {
		configByType[inst.Type] = inst.Config
	}

	// For each registered type, instantiate a driver.
	for _, t := range types {
		reg, ok := reg.Get(t)
		if !ok {
			return nil, fmt.Errorf("provider type %q not found in registry", t)
		}

		// Get config for this type, or use zero-value proto if not configured.
		cfg := reg.ConfigProto()
		if rawConfig, ok := configByType[t]; ok {
			if err := decodeConfig(rawConfig, cfg); err != nil {
				return nil, fmt.Errorf("decode config for provider type %q: %w", t, err)
			}
		}

		// Instantiate the driver.
		driver, err := reg.NewDriver(cfg)
		if err != nil {
			return nil, fmt.Errorf("create driver for provider type %q: %w", t, err)
		}

		drivers = append(drivers, driver)
	}

	return drivers, nil
}

// decodeConfig does a basic map[string]any → struct decode using JSON round-trip.
func decodeConfig(raw map[string]any, target any) error {
	if len(raw) == 0 {
		return nil
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}
	return nil
}
