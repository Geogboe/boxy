package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/Geogboe/boxy/internal/agentserver"
	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/pki"
	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	defaultListenAddr     = ":9090"
	defaultGRPCListenAddr = ":9091"
)

type serveOpts struct {
	configPath string
	listen     string
	ui         bool
	grpcListen string
	insecure   bool
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
	cmd.Flags().StringVar(&opts.grpcListen, "grpc-listen", "", "agent gRPC listen address (default :9091)")
	// Deliberately a flag only — never a boxy.yaml field — so a stale or
	// copy-pasted config can't silently disable mTLS in a real deployment.
	cmd.Flags().BoolVar(&opts.insecure, "insecure", false, "serve agent gRPC without TLS/mTLS (local development only)")

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
	doneConfig, failConfig := ui.step("Loading config")
	cfg, cfgPath, err := loadConfig(opts.configPath)
	if err != nil {
		failConfig(err.Error())
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
	doneProviders, failProviders := ui.step("Registering providers")
	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		failProviders(err.Error())
		return fmt.Errorf("register builtin providers: %w", err)
	}
	doneProviders(strings.Join(providerTypes(reg), ", "))

	// Validate
	doneValidate, failValidate := ui.step("Validating provider config")
	if err := reg.ValidateInstances(ctx, cfg.Providers); err != nil {
		failValidate(err.Error())
		return fmt.Errorf("validate providers: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		failValidate(err.Error())
		return err
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

	doneState, failState := ui.step("Opening state")
	st, statePath, err := openServeStore(cfgPath)
	if err != nil {
		failState(err.Error())
		return err
	}
	doneState(statePath)

	// Drivers + embedded agent
	doneAgent, failAgent := ui.step("Starting embedded agent")
	drivers, err := buildDrivers(reg, cfg.Providers)
	if err != nil {
		failAgent(err.Error())
		return fmt.Errorf("build drivers: %w", err)
	}
	embeddedAgent, err := agentsdk.NewEmbeddedAgent("embedded", "Embedded Agent", drivers...)
	if err != nil {
		failAgent(err.Error())
		return fmt.Errorf("create embedded agent: %w", err)
	}
	doneAgent(fmt.Sprintf("%d drivers", len(drivers)))

	// The registry starts with just the embedded agent; remote agents
	// register themselves here too once they connect (see
	// docs/adr/0005-remote-agent-transport-and-registration.md).
	agentRegistry := pool.NewAgentRegistry()
	if err := agentRegistry.Register(embeddedAgent); err != nil {
		failAgent(err.Error())
		return fmt.Errorf("register embedded agent: %w", err)
	}

	// Use AgentProvisioner to route pool operations through the registry.
	provisioner := &pool.AgentProvisioner{
		Registry:  agentRegistry,
		Specs:     specsMap,
		Providers: providersMap,
	}
	poolMgr := pool.New(st, provisioner)
	sandboxMgr := sandbox.New(st, provisioner)
	sandboxFulfiller := sandbox.NewFulfiller(st, poolMgr, sandboxMgr)
	sandboxDeleter := sandbox.NewDeletionReconciler(st, poolMgr)

	// Pools
	donePools, failPools := ui.step("Initializing pools")
	poolNames, err := seedConfiguredPools(ctx, st, cfg.Pools)
	if err != nil {
		failPools(err.Error())
		return err
	}
	donePools(fmt.Sprintf("%d pool(s)", len(poolNames)))

	// Resolve listen address: flag > config > default
	listenAddr := resolveListenAddr(opts, cmd, cfg)

	// Resolve UI enabled: flag > config > default (true)
	uiEnabled := resolveUIEnabled(opts, cmd, cfg)

	grpcListenAddr := resolveGRPCListenAddr(opts, cmd, cfg)
	heartbeatInterval, err := cfg.Server.EffectiveAgentHeartbeatInterval()
	if err != nil {
		return err
	}

	// Agent transport: private CA + mTLS gRPC listener (ADR-0005).
	doneTLS, failTLS := ui.step("Setting up agent CA/TLS")
	grpcSrv, agentSrv, err := buildAgentGRPCServer(st, agentRegistry, filepath.Dir(statePath), grpcListenAddr, heartbeatInterval, opts.insecure)
	if err != nil {
		failTLS(err.Error())
		return err
	}
	if opts.insecure {
		doneTLS("INSECURE (no TLS — local development only)")
	} else {
		doneTLS("private CA + mTLS")
	}

	srv := server.New(st, sandboxMgr, poolMgr, agentSrv, listenAddr, uiEnabled)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return srv.Start(ctx)
	})
	g.Go(func() error {
		return serveAgentGRPC(ctx, grpcSrv, grpcListenAddr)
	})
	g.Go(func() error {
		agentSrv.RunHeartbeatMonitor(ctx)
		return nil
	})
	g.Go(func() error {
		return serveLoop(ctx, poolMgr, sandboxDeleter, sandboxFulfiller, poolNames, ui)
	})

	printServeBanner(listenAddr, uiEnabled, len(cfg.Pools))

	return g.Wait()
}

// buildAgentGRPCServer bootstraps the private CA and server cert under the
// same .boxy/ directory that holds state.json, and constructs the gRPC
// server hosting the AgentTransport service. TLS uses
// VerifyClientCertIfGiven rather than RequireAndVerifyClientCert: a
// first-time registrant has no client cert yet (it authenticates with a
// single-use token instead, and receives its cert in the response), while
// any presented cert must chain to boxy's own CA. The handler enforces
// that a connection without a verified cert must carry a valid token.
func buildAgentGRPCServer(st store.Store, registry *pool.AgentRegistry, boxyDir, listenAddr string, heartbeatInterval time.Duration, insecureMode bool) (*grpc.Server, *agentserver.Server, error) {
	ca, err := pki.EnsureCA(boxyDir)
	if err != nil {
		return nil, nil, fmt.Errorf("ensure CA: %w", err)
	}

	agentSrv := agentserver.New(st, registry, ca, heartbeatInterval)

	var serverOpts []grpc.ServerOption
	if insecureMode {
		slog.Warn("agent gRPC transport running WITHOUT TLS (--insecure); never use this outside local development")
	} else {
		sans := []string{"localhost", "127.0.0.1"}
		if host, _, splitErr := net.SplitHostPort(listenAddr); splitErr == nil && host != "" && host != "0.0.0.0" && host != "::" {
			sans = append(sans, host)
		}
		serverCert, err := pki.IssueServerCert(ca, boxyDir, sans)
		if err != nil {
			return nil, nil, fmt.Errorf("issue server cert: %w", err)
		}
		tlsCert, err := tls.X509KeyPair(serverCert.CertPEM, serverCert.KeyPEM)
		if err != nil {
			return nil, nil, fmt.Errorf("load server key pair: %w", err)
		}
		clientCAs := x509.NewCertPool()
		clientCAs.AppendCertsFromPEM(ca.CertPEM)
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			ClientAuth:   tls.VerifyClientCertIfGiven,
			ClientCAs:    clientCAs,
			MinVersion:   tls.VersionTLS13,
		})))
	}

	grpcSrv := grpc.NewServer(serverOpts...)
	boxyagentv1.RegisterAgentTransportServiceServer(grpcSrv, agentSrv)
	return grpcSrv, agentSrv, nil
}

// serveAgentGRPC runs the agent gRPC listener with the same
// shutdown-on-context-cancel pattern internal/server.Server.Start uses.
func serveAgentGRPC(ctx context.Context, grpcSrv *grpc.Server, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen agent grpc %q: %w", addr, err)
	}
	go func() {
		<-ctx.Done()
		grpcSrv.GracefulStop()
	}()
	if err := grpcSrv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve agent grpc: %w", err)
	}
	return nil
}

// resolveGRPCListenAddr picks the agent gRPC listen address with
// precedence: explicit --grpc-listen flag > config server.grpc_listen >
// default :9091.
func resolveGRPCListenAddr(opts serveOpts, cmd *cobra.Command, cfg boxyconfig.Config) string {
	if cmd.Flags().Changed("grpc-listen") {
		return opts.grpcListen
	}
	if cfg.Server.GRPCListen != "" {
		return cfg.Server.GRPCListen
	}
	return defaultGRPCListenAddr
}

func seedConfiguredPools(ctx context.Context, st store.Store, specs []boxyconfig.PoolSpec) ([]model.PoolName, error) {
	resources, err := st.ListResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list resources for pool seeding: %w", err)
	}

	poolNames := make([]model.PoolName, 0, len(specs))
	for _, spec := range specs {
		p, err := poolSpecToModel(spec)
		if err != nil {
			return nil, fmt.Errorf("create pool model for %q: %w", spec.Name, err)
		}

		var fallback []model.Resource
		existing, err := st.GetPool(ctx, p.Name)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("get existing pool %q: %w", p.Name, err)
		}
		if err == nil {
			fallback = existing.Inventory.Resources
			p.Drain.Operator = existing.Drain.Operator
		}

		rebuilt, report, err := pool.RebuildReadyInventory(p, resources, fallback)
		if err != nil {
			return nil, fmt.Errorf("rebuild pool %q inventory: %w", p.Name, err)
		}
		for _, skipped := range report.Skipped {
			slog.Warn(
				"skipping persisted pool resource during startup",
				"pool", p.Name,
				"resource", skipped.ResourceID,
				"reason", skipped.Reason,
			)
		}
		if err := st.PutPool(ctx, rebuilt); err != nil {
			return nil, fmt.Errorf("seed pool %q: %w", spec.Name, err)
		}
		poolNames = append(poolNames, rebuilt.Name)
	}

	return poolNames, nil
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

func openServeStore(cfgPath string) (store.Store, string, error) {
	statePath, err := serveStatePath(cfgPath)
	if err != nil {
		return nil, "", err
	}
	st, err := store.NewDiskStore(statePath)
	if err != nil {
		return nil, "", err
	}
	return st, statePath, nil
}

func serveStatePath(cfgPath string) (string, error) {
	if cfgPath != "" {
		return filepath.Join(filepath.Dir(cfgPath), ".boxy", "state.json"), nil
	}

	wd, err := effectiveWD()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Join(wd, ".boxy", "state.json"), nil
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

func serveLoop(
	ctx context.Context,
	poolMgr servePoolReconciler,
	sandboxDeleter serveSandboxReconciler,
	sandboxFulfiller serveSandboxReconciler,
	poolNames []model.PoolName,
	ui *serveUI,
) error {
	const tickEvery = 10 * time.Second

	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ui.shutdown()
			return nil
		case <-ticker.C:
			serveReconcilePass(ctx, poolMgr, sandboxDeleter, sandboxFulfiller, poolNames, ui)
		}
	}
}

type servePoolReconciler interface {
	Reconcile(ctx context.Context, poolName model.PoolName) error
}

type serveSandboxReconciler interface {
	Reconcile(ctx context.Context) error
}

type serveSandboxReconcilerFunc func(ctx context.Context) error

func (f serveSandboxReconcilerFunc) Reconcile(ctx context.Context) error {
	return f(ctx)
}

func serveReconcilePass(
	ctx context.Context,
	poolMgr servePoolReconciler,
	sandboxDeleter serveSandboxReconciler,
	sandboxFulfiller serveSandboxReconciler,
	poolNames []model.PoolName,
	ui *serveUI,
) {
	reconcilePools := func() {
		for _, name := range poolNames {
			if err := poolMgr.Reconcile(ctx, name); err != nil {
				ui.reconcileError(name, err)
			}
		}
	}

	if sandboxDeleter != nil {
		if err := sandboxDeleter.Reconcile(ctx); err != nil {
			ui.printErr(err)
		}
	}
	reconcilePools()
	if sandboxFulfiller != nil {
		if err := sandboxFulfiller.Reconcile(ctx); err != nil {
			ui.printErr(err)
		}
	}
	reconcilePools()
}

// poolSpecToModel converts a config PoolSpec into a runtime model.Pool.
// Initializes the pool's inventory with the expected type and profile.
func poolSpecToModel(spec boxyconfig.PoolSpec) (model.Pool, error) {
	expectedType, err := boxyconfig.ResolvePoolExpectedType(spec.Type)
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
		Drain: model.PoolDrainState{
			ConfigDeclared: policy.Preheat.ConfiguresDrain(),
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
