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
	server    string
	providers []string
	token     string
	name      string
	caCert    string
	dataDir   string
	insecure  bool
}

func newAgentServeCommand() *cobra.Command {
	var opts agentServeOpts

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run this host as a remote boxy agent (dials the server, executes provider operations)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentServe(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.server, "server", "", "boxy server gRPC address (host:port), required")
	cmd.Flags().StringSliceVar(&opts.providers, "providers", nil, "provider types this agent hosts (e.g. docker,hyperv), required")
	cmd.Flags().StringVar(&opts.token, "token", "", "single-use registration token (first connection only)")
	cmd.Flags().StringVar(&opts.name, "name", "", "human-readable agent name (default: hostname)")
	cmd.Flags().StringVar(&opts.caCert, "ca-cert", "", "path to the server's CA certificate, required for the first (token) connection unless --insecure")
	cmd.Flags().StringVar(&opts.dataDir, "data-dir", "", "directory for the agent's issued credentials (default .boxy-agent in cwd)")
	cmd.Flags().BoolVar(&opts.insecure, "insecure", false, "connect without TLS (local development only)")
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("providers")

	return cmd
}

func runAgentServe(ctx context.Context, opts agentServeOpts) error {
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
	for _, p := range opts.providers {
		p = strings.TrimSpace(p)
		if p != "" {
			providerTypes = append(providerTypes, providersdk.Type(p))
		}
	}
	if len(providerTypes) == 0 {
		return fmt.Errorf("--providers must name at least one provider type")
	}

	drivers, err := buildAgentDrivers(providerTypes)
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
				}
			}
		},
	})
}

// buildAgentDrivers instantiates a driver for exactly the requested
// provider types (unlike the daemon's buildDrivers, which builds every
// registered type). Provider connection config is auto-discovered by each
// driver, per the existing provider model.
func buildAgentDrivers(types []providersdk.Type) (agentsdk.DriverSet, error) {
	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return nil, fmt.Errorf("register providers: %w", err)
	}

	drivers := make(agentsdk.DriverSet, len(types))
	for _, t := range types {
		registration, ok := reg.Get(t)
		if !ok {
			return nil, fmt.Errorf("unknown provider type %q (known: %v)", t, reg.Types())
		}
		driver, err := registration.NewDriver(registration.ConfigProto())
		if err != nil {
			return nil, fmt.Errorf("create driver for provider type %q: %w", t, err)
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
		if err := os.WriteFile(filepath.Join(dataDir, name), data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}
