package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Geogboe/boxy/internal/agentserver"
	"github.com/Geogboe/boxy/internal/auth"
	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/internal/svcmgr"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/lifecycle"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/pki"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
	"github.com/Geogboe/boxy/pkg/resourcepack"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	defaultListenAddr     = ":9090"
	defaultGRPCListenAddr = ":9091"
)

type serveOpts struct {
	configPath        string
	listen            string
	ui                bool
	grpcListen        string
	grpcCertSANs      []string
	insecure          bool
	serviceConfigPath string
}

func newServeCommand() *cobra.Command {
	var opts serveOpts

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Boxy daemon (API server + reconcile loop)",
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, err := svcmgr.RunAsWindowsService("boxy-serve", func(ctx context.Context) error {
				return runServe(ctx, opts, cmd)
			})
			if handled {
				return err
			}
			return runServe(cmd.Context(), opts, cmd)
		},
	}

	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	cmd.Flags().StringVar(&opts.listen, "listen", "", "HTTP listen address (default :9090)")
	cmd.Flags().BoolVar(&opts.ui, "ui", true, "enable web dashboard UI")
	cmd.Flags().StringVar(&opts.grpcListen, "grpc-listen", "", "agent gRPC listen address (default :9091)")
	cmd.Flags().StringArrayVar(&opts.grpcCertSANs, "grpc-cert-san", nil, "extra DNS name or IP to include in the agent gRPC server certificate SANs (repeatable)")
	// Deliberately a flag only — never a boxy.yaml field — so a stale or
	// copy-pasted config can't silently disable mTLS in a real deployment.
	cmd.Flags().BoolVar(&opts.insecure, "insecure", false, "serve agent gRPC without TLS/mTLS (local development only)")
	cmd.Flags().StringVar(&opts.serviceConfigPath, "service-config", "", "load flags from a service config file written by `boxy serve service install` instead of the flags above")

	cmd.AddCommand(newServeServiceCommand())

	return cmd
}

// resolveServeOpts returns the effective opts to run with: loaded entirely
// from --service-config's file when set, otherwise opts unchanged. Unlike
// resolveAgentServeOpts, serve has no required flags to validate here — its
// flags are all optional with defaults already applied downstream by
// resolveListenAddr/resolveUIEnabled/resolveGRPCListenAddr/resolveGRPCCertSANs.
//
// Those four resolve funcs normally gate on cmd.Flags().Changed(...), which
// is always false for a --service-config invocation (the individual flags
// were never set on this process's cmdline — only --service-config was).
// So the opts this function returns must already be the final, concrete
// values: listen/grpcListen fall back to the same defaults
// resolveListenAddr/resolveGRPCListenAddr would apply, since `serve service
// install` persists the raw --listen/--grpc-listen flag value (which is ""
// when the operator didn't pass one) rather than a pre-resolved default.
// The resolve funcs are still given an extra `opts.serviceConfigPath != ""`
// gate (alongside Changed()) so they return these values instead of
// falling through to boxy.yaml's server config, which a service install
// deliberately bypasses in favor of its own persisted snapshot.
func resolveServeOpts(opts serveOpts) (serveOpts, error) {
	if opts.serviceConfigPath == "" {
		return opts, nil
	}
	cfg, err := loadServeServiceConfig(opts.serviceConfigPath)
	if err != nil {
		return serveOpts{}, fmt.Errorf("load --service-config %q: %w", opts.serviceConfigPath, err)
	}
	listen := cfg.Listen
	if listen == "" {
		listen = defaultListenAddr
	}
	grpcListen := cfg.GRPCListen
	if grpcListen == "" {
		grpcListen = defaultGRPCListenAddr
	}
	return serveOpts{
		configPath:        cfg.ConfigPath,
		listen:            listen,
		ui:                cfg.UI,
		grpcListen:        grpcListen,
		grpcCertSANs:      cfg.GRPCCertSANs,
		insecure:          cfg.Insecure,
		serviceConfigPath: opts.serviceConfigPath,
	}, nil
}

func runServe(ctx context.Context, opts serveOpts, cmd *cobra.Command) error {
	opts, err := resolveServeOpts(opts)
	if err != nil {
		return err
	}

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
	poolSpecs, err := cfg.ResolvePoolSpecs()
	if err != nil {
		failValidate(err.Error())
		return fmt.Errorf("resolve pool specs: %w", err)
	}
	packageRegistry, err := cfg.PackageRegistry(ctx)
	if err != nil {
		failValidate(err.Error())
		return fmt.Errorf("build resource package registry: %w", err)
	}
	catalogSource := server.NewStaticCatalogSource(catalogSnapshotFromConfig(cfg, poolSpecs))
	doneValidate(fmt.Sprintf("%d configured", len(cfg.Providers)))

	// Build lookup maps for the DriverProvisioner.
	specsMap := make(map[model.PoolName]boxyconfig.PoolSpec, len(poolSpecs))
	for _, spec := range poolSpecs {
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

	bootstrapped, err := bootstrapLocalAdmin(ctx, st, statePath)
	if err != nil {
		return fmt.Errorf("bootstrap local admin account: %w", err)
	}
	if bootstrapped {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Bootstrapped a local admin web-UI account. Run `boxy admin bootstrap-password` to view the one-time password.")
	}

	guestSecrets, err := openConfiguredSecretStore(cfg.Server.Secrets, statePath, requiresGuestSecretBackend(cfg))
	if err != nil {
		return fmt.Errorf("open secret backend: %w", err)
	}

	// Drivers + embedded agent
	doneAgent, failAgent := ui.step("Starting embedded agent")
	drivers, err := buildDrivers(reg, cfg.Providers, cfgPath)
	if err != nil {
		failAgent(err.Error())
		return fmt.Errorf("build drivers: %w", err)
	}
	configureEmbeddedGuestBootstrapResolvers(drivers, st, guestSecrets, specsMap)
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
		Registry:      agentRegistry,
		Specs:         specsMap,
		Providers:     providersMap,
		GuestSecrets:  guestSecrets,
		PackageEngine: &resourcepack.Engine{Registry: packageRegistry},
	}
	poolMgr := pool.New(st, provisioner)
	poolMgr.SetPromoter(&pool.PromotionService{
		Store:           st,
		Provisioner:     provisioner,
		Compatibility:   provisioner,
		Packages:        provisioner,
		Personalizer:    provisioner,
		Secrets:         guestSecrets,
		TemplateParents: cfg.TemplateParents(),
	})
	poolMgr.SetGuestSecretStore(guestSecrets)
	// Shares agentRegistry's per-agent lock with RunAgentReconciliation's
	// sweep below, closing a race exposed by fast ResourceLister drivers
	// (see pool.ProvisionLocker's doc comment).
	poolMgr.SetProvisionLocker(agentRegistry)
	eventStore, ok := st.(lifecycle.EventStore)
	if !ok {
		return fmt.Errorf("state store does not support lifecycle events")
	}
	admissionPublisher := &pool.EventPublisher{Events: eventStore}
	admissionHandler := &pool.AdmissionHandler{
		Store:        st,
		Secrets:      guestSecrets,
		Personalizer: provisioner,
		Failures:     poolMgr,
		Packages:     provisioner,
	}
	admissionDispatcher := lifecycle.NewDispatcher(eventStore, admissionHandler)
	poolMgr.SetAdmissionPublisher(admissionPublisher)
	sandboxMgr := sandbox.New(st, provisioner)
	sandboxFulfiller := sandbox.NewFulfiller(st, poolMgr, sandboxMgr)
	sandboxDeleter := sandbox.NewDeletionReconciler(st, poolMgr)
	sessionSweeper := server.NewSessionSweeper(st)

	// Pools
	donePools, failPools := ui.step("Initializing pools")
	poolNames, err := seedConfiguredPools(ctx, st, poolSpecs)
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
	grpcCertSANs := resolveGRPCCertSANs(opts, cmd, cfg)
	// The shared Boxy server certificate serves both agent gRPC and the REST
	// API, so include hostnames from both listen addresses in its SAN set.
	certSANs := append([]string(nil), grpcCertSANs...)
	certSANs = append(certSANs, agentCertSANs(listenAddr, nil)...)
	heartbeatInterval, err := cfg.Server.EffectiveAgentHeartbeatInterval()
	if err != nil {
		return err
	}

	// The embedded agent registers once above and, unlike a remote agent,
	// never reconnects to trigger internal/agentserver's per-connection
	// reconciliation sweep — without a sweep of its own it would never
	// adopt an orphan the inline Create-failure path couldn't quarantine
	// (e.g. resolveCreatedVMID failing after Start-VM succeeds; see #174).
	// ctx is runServe's own context, so this stops on shutdown like every
	// other server-lifetime goroutine here.
	go pool.RunAgentReconciliation(ctx, st, agentRegistry, embeddedAgent.Info().ID, heartbeatInterval, pool.DefaultReconciliationPassTimeout, nil)

	// Agent transport: private CA + mTLS gRPC listener (ADR-0005).
	doneTLS, failTLS := ui.step("Setting up agent CA/TLS")
	grpcSrv, agentSrv, err := buildAgentGRPCServer(st, agentRegistry, poolMgr, filepath.Dir(statePath), grpcListenAddr, heartbeatInterval, opts.insecure, certSANs)
	if err != nil {
		failTLS(err.Error())
		return err
	}
	agentSrv.SetGuestBootstrapResolver(func(ctx context.Context, resource model.Resource) (providersdk.GuestBootstrapCredential, error) {
		return resolveGuestBootstrap(ctx, st, guestSecrets, specsMap, resource)
	})
	if opts.insecure {
		doneTLS("INSECURE (no TLS — local development only)")
	} else {
		doneTLS("private CA + mTLS")
	}

	var httpCertPEM, httpKeyPEM []byte
	if !opts.insecure {
		ca, err := pki.EnsureCA(filepath.Dir(statePath))
		if err != nil {
			return fmt.Errorf("ensure REST API CA: %w", err)
		}
		serverCert, err := pki.IssueServerCert(ca, filepath.Dir(statePath), agentCertSANs(grpcListenAddr, certSANs))
		if err != nil {
			return fmt.Errorf("issue REST API server cert: %w", err)
		}
		httpCertPEM = serverCert.CertPEM
		httpKeyPEM = serverCert.KeyPEM
	}

	oidcOptions, err := buildOIDCOptions(ctx, cfg.Server.OIDC)
	if err != nil {
		return fmt.Errorf("configure OIDC: %w", err)
	}
	sessionTTL, err := cfg.Server.OIDC.EffectiveSessionTTL()
	if err != nil {
		return fmt.Errorf("configure OIDC: %w", err)
	}

	srv := server.NewWithOptions(st, sandboxMgr, poolMgr, agentSrv, listenAddr, uiEnabled, server.ServerOptions{
		AuthRequired: true,
		InsecureHTTP: opts.insecure,
		TLSCertPEM:   httpCertPEM,
		TLSKeyPEM:    httpKeyPEM,
		Executor:     provisioner,
		GuestSecrets: guestSecrets,
		Catalog:      catalogSource,
		OIDC:         oidcOptions,
		SessionTTL:   sessionTTL,
	})

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
		return admissionDispatcher.Run(ctx)
	})
	g.Go(func() error {
		return serveLoop(ctx, poolMgr, sandboxDeleter, sandboxFulfiller, sessionSweeper, poolNames, ui)
	})

	printServeBanner(listenAddr, uiEnabled, len(poolSpecs), opts.insecure)

	return g.Wait()
}

func configureEmbeddedGuestBootstrapResolvers(drivers []providersdk.Driver, st store.Store, guestSecrets boxysecrets.Store, specs map[model.PoolName]boxyconfig.PoolSpec) {
	for _, driver := range drivers {
		configurer, ok := driver.(interface {
			SetGuestBootstrapResolver(providersdk.GuestBootstrapResolver)
		})
		if !ok {
			continue
		}
		configurer.SetGuestBootstrapResolver(func(ctx context.Context, resourceID string) (providersdk.GuestBootstrapCredential, error) {
			resource, err := st.GetResource(ctx, model.ResourceID(resourceID))
			if err != nil {
				return providersdk.GuestBootstrapCredential{}, fmt.Errorf("get resource: %w", err)
			}
			return resolveGuestBootstrap(ctx, st, guestSecrets, specs, resource)
		})
	}
}

// agentCertSANs assembles the full SAN list for the agent gRPC server
// certificate: the always-present localhost/127.0.0.1 entries, the host
// parsed from listenAddr (skipped for a wildcard/empty bind — there's no
// single literal hostname to add), and any operator-configured extra SANs
// (trimmed, with empty entries dropped and exact-string-match duplicates
// removed — case-sensitive dedup only, not full DNS-case-insensitive
// normalization).
func agentCertSANs(listenAddr string, extra []string) []string {
	sans := []string{"localhost", "127.0.0.1"}
	if host, _, splitErr := net.SplitHostPort(listenAddr); splitErr == nil && host != "" && host != "0.0.0.0" && host != "::" {
		sans = append(sans, host)
	}
	seen := make(map[string]struct{}, len(sans))
	for _, s := range sans {
		seen[s] = struct{}{}
	}
	for _, e := range extra {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		sans = append(sans, e)
	}
	return sans
}

// buildAgentGRPCServer bootstraps the private CA and server cert under the
// same .boxy/ directory that holds state.json, and constructs the gRPC
// server hosting the AgentTransport service. TLS uses
// VerifyClientCertIfGiven rather than RequireAndVerifyClientCert: a
// first-time registrant has no client cert yet (it authenticates with a
// single-use token instead, and receives its cert in the response), while
// any presented cert must chain to boxy's own CA. The handler enforces
// that a connection without a verified cert must carry a valid token.
func buildAgentGRPCServer(st store.Store, registry *pool.AgentRegistry, forceOrphaner agentserver.ResourceForceOrphaner, boxyDir, listenAddr string, heartbeatInterval time.Duration, insecureMode bool, extraCertSANs []string) (*grpc.Server, *agentserver.Server, error) {
	ca, err := pki.EnsureCA(boxyDir)
	if err != nil {
		return nil, nil, fmt.Errorf("ensure CA: %w", err)
	}

	agentSrv := agentserver.New(st, registry, ca, heartbeatInterval, forceOrphaner, Version)

	var serverOpts []grpc.ServerOption
	if insecureMode {
		slog.Warn("agent gRPC transport running WITHOUT TLS (--insecure); never use this outside local development")
	} else {
		sans := agentCertSANs(listenAddr, extraCertSANs)
		serverCert, err := pki.IssueServerCert(ca, boxyDir, sans)
		if err != nil {
			return nil, nil, fmt.Errorf("issue server cert: %w", err)
		}
		tlsCert, err := tls.X509KeyPair(serverCert.CertPEM, serverCert.KeyPEM)
		if err != nil {
			return nil, nil, fmt.Errorf("load server key pair: %w", err)
		}
		clientCAs, err := buildClientCAPool(ca.CertPEM)
		if err != nil {
			return nil, nil, fmt.Errorf("build client CA pool: %w", err)
		}
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

// buildClientCAPool parses caPEM into a cert pool for verifying client
// certificates presented over the agent gRPC transport. Mirrors the
// equivalent CA-parsing check on the agent side (agent_serve.go's dial
// credentials) so a corrupt/empty CA cert fails loudly instead of silently
// coming up with a non-functional (empty) client trust store.
func buildClientCAPool(caPEM []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no valid certificates found in CA cert")
	}
	return pool, nil
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
	if cmd.Flags().Changed("grpc-listen") || opts.serviceConfigPath != "" {
		return opts.grpcListen
	}
	if cfg.Server.GRPCListen != "" {
		return cfg.Server.GRPCListen
	}
	return defaultGRPCListenAddr
}

// resolveGRPCCertSANs picks the extra SAN list for the agent gRPC server
// certificate with precedence: explicit --grpc-cert-san flag(s) (fully
// replace, not merge with, config) > config server.grpc_cert_sans > none.
func resolveGRPCCertSANs(opts serveOpts, cmd *cobra.Command, cfg boxyconfig.Config) []string {
	if cmd.Flags().Changed("grpc-cert-san") || opts.serviceConfigPath != "" {
		return opts.grpcCertSANs
	}
	return cfg.Server.GRPCCertSANs
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
	if cmd.Flags().Changed("listen") || opts.serviceConfigPath != "" {
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
	if cmd.Flags().Changed("ui") || opts.serviceConfigPath != "" {
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

// buildOIDCOptions returns nil (OIDC not configured -- the web UI's
// bootstrapped local admin account remains the only login path) when
// spec.Configured() is false, so this only does a live discovery-document
// fetch against the issuer when OIDC is actually set up.
func buildOIDCOptions(ctx context.Context, spec boxyconfig.OIDCSpec) (*server.OIDCOptions, error) {
	if !spec.Configured() {
		return nil, nil
	}
	clientSecret, err := providersdk.ResolveSecretRef(ctx, providersdk.SecretRef(spec.ClientSecret))
	if err != nil {
		return nil, fmt.Errorf("resolve server.oidc.client_secret: %w", err)
	}
	provider, err := oidc.NewProvider(ctx, spec.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer %q: %w", spec.Issuer, err)
	}
	roleMapping := make(map[string]string, len(spec.RoleMapping))
	for claimValue, role := range spec.RoleMapping {
		roleMapping[claimValue] = role
	}
	personalKeyMaxTTL, err := spec.EffectivePersonalKeyMaxTTL()
	if err != nil {
		return nil, err
	}
	var cliVerifier *oidc.IDTokenVerifier
	if spec.CLIClientID != "" {
		// A separate verifier: an ID token minted for the CLI's own
		// device-flow client carries CLIClientID as its audience, not
		// spec.ClientID (the confidential web client) -- see
		// server.OIDCOptions.CLIVerifier's doc comment.
		cliVerifier = provider.Verifier(&oidc.Config{ClientID: spec.CLIClientID})
	}
	return &server.OIDCOptions{
		Issuer: spec.Issuer,
		OAuth2: oauth2.Config{
			ClientID:     spec.ClientID,
			ClientSecret: clientSecret,
			RedirectURL:  spec.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		Verifier:          provider.Verifier(&oidc.Config{ClientID: spec.ClientID}),
		RoleClaim:         spec.RoleClaim,
		RoleMapping:       roleMapping,
		DefaultRole:       model.APIKeyRole(spec.DefaultRole),
		CLIClientID:       spec.CLIClientID,
		CLIVerifier:       cliVerifier,
		PersonalKeyMaxTTL: personalKeyMaxTTL,
	}, nil
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

// bootstrapPasswordFileName lives next to state.json in the same .boxy/
// directory. See bootstrapLocalAdmin's doc comment for the one-time-read
// contract `boxy admin bootstrap-password` relies on.
const bootstrapPasswordFileName = "bootstrap-admin-password"

func serveBootstrapPasswordPath(cfgPath string) (string, error) {
	statePath, err := serveStatePath(cfgPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), bootstrapPasswordFileName), nil
}

// bootstrapLocalAdmin ensures exactly one model.LocalAdminAccount exists in
// st, generating and persisting (as a bcrypt hash) a random password the
// very first time a daemon starts against this store. The raw password is
// never persisted to st — it's written once to a restricted-permission
// file next to state.json, for `boxy admin bootstrap-password` to read.
// Idempotent: a daemon restart against an already-bootstrapped store is a
// no-op (reports bootstrapped=false), matching the "exactly once" contract
// the loopback API-key bootstrap endpoint already established (see
// internal/server's handleBootstrapAPIKey and ADR-0007).
func bootstrapLocalAdmin(ctx context.Context, st store.Store, statePath string) (bootstrapped bool, err error) {
	if _, err := st.GetLocalAdmin(ctx); err == nil {
		return false, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, fmt.Errorf("check local admin bootstrap state: %w", err)
	}

	rawPassword, err := auth.GenerateBootstrapPassword()
	if err != nil {
		return false, err
	}
	hash, err := auth.HashPassword(rawPassword)
	if err != nil {
		return false, err
	}
	if err := st.PutLocalAdmin(ctx, model.LocalAdminAccount{
		Username:     model.LocalAdminUsername,
		PasswordHash: hash,
		CreatedAt:    time.Now(),
	}); err != nil {
		return false, fmt.Errorf("persist local admin account: %w", err)
	}

	passwordPath := filepath.Join(filepath.Dir(statePath), bootstrapPasswordFileName)
	if err := os.MkdirAll(filepath.Dir(passwordPath), 0o700); err != nil {
		return false, fmt.Errorf("create state directory %q: %w", filepath.Dir(passwordPath), err)
	}
	// New file (this whole function only runs once, gated by GetLocalAdmin
	// above), so WriteFile's mode argument actually takes effect — see
	// ADR-0009's note that mode is ignored only when rewriting a
	// pre-existing file.
	if err := os.WriteFile(passwordPath, []byte(rawPassword+"\n"), 0o600); err != nil {
		return false, fmt.Errorf("write bootstrap password file %q: %w", passwordPath, err)
	}
	return true, nil
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
	sessionSweeper serveSandboxReconciler,
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
			serveReconcilePass(ctx, poolMgr, sandboxDeleter, sandboxFulfiller, sessionSweeper, poolNames, ui)
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
	sessionSweeper serveSandboxReconciler,
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
	if sessionSweeper != nil {
		if err := sessionSweeper.Reconcile(ctx); err != nil {
			ui.printErr(err)
		}
	}
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
		Name:     model.PoolName(spec.Name),
		Template: spec.Template,
		Source:   spec.Source,
		Packages: append([]string(nil), spec.Packages...),
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
func printServeBanner(listenAddr string, uiEnabled bool, poolCount int, insecure bool) {
	host := displayAddr(listenAddr)
	scheme := "https"
	if insecure {
		scheme = "http"
	}

	pterm.Println()
	pterm.Bold.Printfln("  Boxy is running")
	pterm.Println()
	if uiEnabled {
		pterm.Printfln("    Dashboard   %s://%s/", scheme, host)
	}
	pterm.Printfln("    API         %s://%s/api/v1/", scheme, host)
	pterm.Printfln("    Health      %s://%s/healthz", scheme, host)
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
//
// cfgPath is the boxy config file instances came from ("" if none); its
// directory is passed to each config's providersdk.RelativePathResolver, if
// implemented (see devfactory.Config.ResolveRelativePaths).
func buildDrivers(reg *providersdk.Registry, instances []providersdk.Instance, cfgPath string) ([]providersdk.Driver, error) {
	baseDir := ""
	if cfgPath != "" {
		baseDir = filepath.Dir(cfgPath)
	}

	types := reg.Types()
	drivers := make([]providersdk.Driver, 0, len(types))

	// Build a map of type -> configured instance for easy lookup.
	configByType := make(map[providersdk.Type]providersdk.Instance)
	for _, inst := range instances {
		if previous, ok := configByType[inst.Type]; ok {
			return nil, fmt.Errorf("provider type %q has multiple configured instances %q and %q; embedded agent drivers are keyed by provider type", inst.Type, previous.Name, inst.Name)
		}
		configByType[inst.Type] = inst
	}

	// For each registered type, instantiate a driver.
	for _, t := range types {
		_, ok := reg.Get(t)
		if !ok {
			return nil, fmt.Errorf("provider type %q not found in registry", t)
		}

		// Get config for this type, or use zero-value proto if not configured.
		instance := configByType[t]
		instance.Type = t
		driver, err := reg.NewDriverFromInstance(instance, baseDir)
		if err != nil {
			return nil, err
		}

		drivers = append(drivers, driver)
	}

	return drivers, nil
}
