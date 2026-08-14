package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/svcmgr"
	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/agentsdk"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
)

// Agent-side credential file names, persisted under --data-dir
// (.boxy-agent/ by default — deliberately distinct from the *server's*
// .boxy/ directory; the two processes usually run on different hosts).
const (
	agentClientCertFile = "client.crt"
	agentClientKeyFile  = "client.key"
	agentCACertFile     = "ca.crt"
)

type agentServeOpts struct {
	server            string
	providers         []string
	providerConfigs   []providersdk.Instance
	configPath        string
	token             string
	name              string
	caCert            string
	dataDir           string
	insecure          bool
	serviceConfigPath string
}

func newAgentServeCommand() *cobra.Command {
	var opts agentServeOpts

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run this host as a remote boxy agent (dials the server, executes provider operations)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, err := svcmgr.RunAsWindowsService("boxy-agent", func(ctx context.Context) error {
				return runAgentServe(ctx, opts)
			})
			if handled {
				return err
			}
			return runAgentServe(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.server, "server", "", "boxy server gRPC address (host:port), required")
	cmd.Flags().StringSliceVar(&opts.providers, "providers", nil, "provider types this agent hosts (e.g. docker,hyperv); optional with --config")
	cmd.Flags().StringVar(&opts.configPath, "config", "", "Boxy config file supplying provider instances")
	cmd.Flags().StringVar(&opts.token, "token", "", "single-use registration token (first connection only)")
	cmd.Flags().StringVar(&opts.name, "name", "", "human-readable agent name (default: hostname)")
	cmd.Flags().StringVar(&opts.caCert, "ca-cert", "", "path to the server's CA certificate, required for the first (token) connection unless --insecure")
	cmd.Flags().StringVar(&opts.dataDir, "data-dir", "", "directory for the agent's issued credentials (default .boxy-agent in cwd)")
	cmd.Flags().BoolVar(&opts.insecure, "insecure", false, "connect without TLS (local development only)")
	cmd.Flags().StringVar(&opts.serviceConfigPath, "service-config", "", "load flags from a service config file written by `boxy agent service install` instead of the flags above")

	return cmd
}

// resolveAgentServeOpts returns the effective opts to run with: loaded
// entirely from --service-config's file when set (a service invocation
// carries no other flags), otherwise opts as given directly, validated
// for the flags that used to be cobra-required (--server, --providers).
func resolveAgentServeOpts(opts agentServeOpts) (agentServeOpts, error) {
	if opts.serviceConfigPath == "" {
		opts.providers = normalizeProviderStrings(opts.providers)
		if opts.server == "" {
			return agentServeOpts{}, fmt.Errorf("--server is required (or pass --service-config)")
		}
		if opts.configPath != "" {
			cfg, err := boxyconfig.LoadFile(opts.configPath)
			if err != nil {
				return agentServeOpts{}, fmt.Errorf("load --config %q: %w", opts.configPath, err)
			}
			instances, err := selectAgentProviderInstances(cfg.Providers, opts.providers)
			if err != nil {
				return agentServeOpts{}, fmt.Errorf("select providers from --config %q: %w", opts.configPath, err)
			}
			opts.providerConfigs = instances
			if len(opts.providers) == 0 {
				opts.providers = providerTypesFromInstances(instances)
			}
		}
		if len(opts.providers) == 0 {
			return agentServeOpts{}, fmt.Errorf("--providers is required unless --config supplies provider instances (or pass --service-config)")
		}
		return opts, nil
	}

	cfg, err := loadAgentServiceConfig(opts.serviceConfigPath)
	if err != nil {
		return agentServeOpts{}, fmt.Errorf("load --service-config %q: %w", opts.serviceConfigPath, err)
	}
	if cfg.Server == "" {
		return agentServeOpts{}, fmt.Errorf("invalid --service-config %q: missing server", opts.serviceConfigPath)
	}
	providers := normalizeProviderStrings(cfg.Providers)
	providerConfigs := cfg.ProviderConfigs
	if len(providerConfigs) != 0 {
		selected, err := selectAgentProviderInstances(providerConfigs, nil)
		if err != nil {
			return agentServeOpts{}, fmt.Errorf("invalid --service-config %q: %w", opts.serviceConfigPath, err)
		}
		providerConfigs = selected
		providers = providerTypesFromInstances(providerConfigs)
	}
	if len(providers) == 0 {
		return agentServeOpts{}, fmt.Errorf("invalid --service-config %q: missing providers", opts.serviceConfigPath)
	}
	return agentServeOpts{
		server:            cfg.Server,
		providers:         providers,
		providerConfigs:   providerConfigs,
		token:             cfg.Token,
		name:              cfg.Name,
		caCert:            cfg.CACert,
		dataDir:           cfg.DataDir,
		insecure:          cfg.Insecure,
		serviceConfigPath: opts.serviceConfigPath,
	}, nil
}

func providerTypesFromInstances(instances []providersdk.Instance) []string {
	providers := make([]string, 0, len(instances))
	for _, instance := range instances {
		providers = append(providers, string(instance.Type))
	}
	return providers
}

func normalizeProviderStrings(values []string) []string {
	providers := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		provider := strings.TrimSpace(raw)
		if provider == "" {
			continue
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		providers = append(providers, provider)
	}
	return providers
}

func selectAgentProviderInstances(instances []providersdk.Instance, requested []string) ([]providersdk.Instance, error) {
	requestedTypes := make(map[providersdk.Type]struct{}, len(requested))
	for _, raw := range requested {
		t := providersdk.Type(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		requestedTypes[t] = struct{}{}
	}
	selected := make([]providersdk.Instance, 0, len(instances))
	for _, instance := range instances {
		if len(requestedTypes) != 0 {
			if _, ok := requestedTypes[instance.Type]; !ok {
				continue
			}
		}
		selected = append(selected, instance)
	}
	if len(requestedTypes) != 0 {
		for t := range requestedTypes {
			count := 0
			for _, instance := range selected {
				if instance.Type == t {
					count++
				}
			}
			if count == 0 {
				return nil, fmt.Errorf("requested provider type %q has no configured instance", t)
			}
			if count > 1 {
				return nil, fmt.Errorf("requested provider type %q has %d configured instances; agent drivers are keyed by provider type", t, count)
			}
		}
	} else {
		seen := make(map[providersdk.Type]struct{}, len(selected))
		for _, instance := range selected {
			if _, ok := seen[instance.Type]; ok {
				return nil, fmt.Errorf("provider type %q has multiple configured instances; agent drivers are keyed by provider type", instance.Type)
			}
			seen[instance.Type] = struct{}{}
		}
	}
	return selected, nil
}

func runAgentServe(ctx context.Context, opts agentServeOpts) error {
	opts, err := resolveAgentServeOpts(opts)
	if err != nil {
		return err
	}

	dataDir := opts.dataDir
	if dataDir == "" {
		wd, err := effectiveWD()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		dataDir = filepath.Join(wd, ".boxy-agent")
	}

	name := opts.name
	if name == "" {
		if hostname, err := os.Hostname(); err == nil {
			name = hostname
		}
	}

	providerTypes := make([]providersdk.Type, 0, len(opts.providers))
	for _, p := range normalizeProviderStrings(opts.providers) {
		providerTypes = append(providerTypes, providersdk.Type(p))
	}
	if len(providerTypes) == 0 {
		return fmt.Errorf("--providers must name at least one provider type")
	}

	drivers, err := buildAgentDrivers(providerTypes, opts.providerConfigs)
	if err != nil {
		return err
	}

	hasCert := agentCredentialsExist(dataDir)
	if !opts.insecure && !hasCert && opts.token == "" {
		return fmt.Errorf("no credentials in %s and no --token: mint one with `boxy agent token create` on the server", dataDir)
	}
	if !opts.insecure && !hasCert && opts.caCert == "" {
		return fmt.Errorf("--ca-cert is required for the first (token) connection: copy the server's .boxy/ca.crt to this host, or use --insecure for local development")
	}

	token := opts.token
	if hasCert {
		// Existing credentials always win: the identity is the cert, and
		// resending a token would either burn a fresh one or fail as used.
		token = ""
	}

	slog.Info("starting boxy agent", "server", opts.server, "providers", providerTypes, "data_dir", dataDir, "insecure", opts.insecure)

	dial := newAgentDialer(opts.server, dataDir, opts.caCert, opts.insecure)
	return agentsdk.Run(ctx, dial, agentsdk.RemoteClientConfig{
		AgentName:     name,
		Token:         token,
		AgentVersion:  Version,
		ProviderTypes: providerTypes,
		Drivers:       drivers,
		OnRegistered: func(resp *boxyagentv1.RegisterResponse) {
			slog.Info("registered with server", "agent_id", resp.GetAgentId())
			if len(resp.GetClientCertificatePem()) > 0 {
				if err := persistAgentCredentials(dataDir, resp); err != nil {
					// Fatal-worthy in spirit (a restart would need a fresh
					// token), but the live session keeps working — surface
					// loudly and keep serving.
					slog.Error("failed to persist issued credentials; reconnects after restart will need a new token", "error", err, "data_dir", dataDir)
				} else if opts.serviceConfigPath != "" {
					if err := scrubAgentServiceConfigToken(opts.serviceConfigPath); err != nil {
						slog.Warn("failed to scrub bootstrap token from service config after registration", "error", err, "path", opts.serviceConfigPath)
					}
				}
			}
		},
	})
}

// buildAgentDrivers instantiates a driver for exactly the requested
// provider types (unlike the daemon's buildDrivers, which builds every
// registered type). When an instance was loaded from --config, its provider
// connection settings are decoded before the driver is constructed; the
// legacy flag-only path receives the provider's zero-value config.
func buildAgentDrivers(types []providersdk.Type, instances []providersdk.Instance) (agentsdk.DriverSet, error) {
	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return nil, fmt.Errorf("register providers: %w", err)
	}

	drivers := make(agentsdk.DriverSet, len(types))
	for _, t := range types {
		instance := providersdk.Instance{Type: t}
		for _, candidate := range instances {
			if candidate.Type == t {
				instance = candidate
				break
			}
		}
		driver, err := reg.NewDriverFromInstance(instance)
		if err != nil {
			if _, ok := reg.Get(t); !ok {
				return nil, fmt.Errorf("unknown provider type %q (known: %v)", t, reg.Types())
			}
			return nil, err
		}
		drivers[t] = driver
	}
	return drivers, nil
}

// newAgentDialer returns a Dialer that rebuilds its TLS credentials on
// every attempt, so a reconnect after the first (token-based) registration
// picks up the freshly persisted client certificate without restarting the
// process. The previous connection is closed before each new dial.
func newAgentDialer(serverAddr, dataDir, caCertPath string, insecureMode bool) agentsdk.Dialer {
	var prevConn *grpc.ClientConn
	return func(ctx context.Context) (boxyagentv1.AgentTransportService_ConnectClient, error) {
		if prevConn != nil {
			_ = prevConn.Close()
			prevConn = nil
		}

		creds, err := agentTransportCredentials(dataDir, caCertPath, insecureMode)
		if err != nil {
			return nil, err
		}

		conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(creds))
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", serverAddr, err)
		}

		stream, err := boxyagentv1.NewAgentTransportServiceClient(conn).Connect(ctx)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("open agent stream: %w", err)
		}
		prevConn = conn
		return stream, nil
	}
}

func agentTransportCredentials(dataDir, caCertPath string, insecureMode bool) (credentials.TransportCredentials, error) {
	if insecureMode {
		return insecure.NewCredentials(), nil
	}

	// The persisted CA (from RegisterResponse) wins over --ca-cert once
	// registration has succeeded.
	caPEM, err := os.ReadFile(filepath.Join(dataDir, agentCACertFile))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read persisted CA cert: %w", err)
		}
		caPEM, err = os.ReadFile(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("read --ca-cert %q: %w", caCertPath, err)
		}
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no valid certificates found in CA cert")
	}

	tlsCfg := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}

	certPath := filepath.Join(dataDir, agentClientCertFile)
	keyPath := filepath.Join(dataDir, agentClientKeyFile)
	if fileExists(certPath) && fileExists(keyPath) {
		clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{clientCert}
	}

	return credentials.NewTLS(tlsCfg), nil
}

func agentCredentialsExist(dataDir string) bool {
	return fileExists(filepath.Join(dataDir, agentClientCertFile)) &&
		fileExists(filepath.Join(dataDir, agentClientKeyFile)) &&
		fileExists(filepath.Join(dataDir, agentCACertFile))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func persistAgentCredentials(dataDir string, resp *boxyagentv1.RegisterResponse) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %q: %w", dataDir, err)
	}
	files := map[string][]byte{
		agentClientCertFile: resp.GetClientCertificatePem(),
		agentClientKeyFile:  resp.GetClientPrivateKeyPem(),
		agentCACertFile:     resp.GetCaCertificatePem(),
	}
	for name, data := range files {
		if len(data) == 0 {
			return fmt.Errorf("server response missing %s material", name)
		}
		path := filepath.Join(dataDir, name)
		// os.WriteFile's mode argument is only applied by the OS when it
		// creates a new file; on rewrite of a pre-existing file (this path
		// runs on every successful registration, including reconnects over
		// already-persisted credentials), the file keeps whatever
		// permissions it already had, silently. name here includes the
		// agent's private key — explicit os.Chmod closes that gap. See #158.
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("set %s permissions: %w", name, err)
		}
	}
	return nil
}
