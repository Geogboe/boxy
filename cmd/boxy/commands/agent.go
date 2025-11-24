package commands

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/pkg/agent"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider/docker"
	"github.com/Geogboe/boxy/pkg/provider/hyperv"
	"github.com/Geogboe/boxy/pkg/provider/mock"
	"github.com/Geogboe/boxy/pkg/provider/scratch/shell"
)

var (
	agentListenAddr string
	agentTLSCert    string
	agentTLSKey     string
	agentTLSCA      string
	agentUseTLS     bool
	agentProviders  []string
)

// agentCmd represents the agent command
var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Agent commands for distributed resource management",
	Long: `Manage Boxy agents that run on remote machines and provide access to local resources.

Agents run on Windows machines (for Hyper-V), Linux machines (for KVM, Docker), or any
host that has virtualization/containerization resources. They expose local providers
via gRPC so the central Boxy service can provision resources remotely.`,
}

// agentServeCmd starts an agent server
var agentServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a Boxy agent server",
	Long: `Start a Boxy agent server that exposes local providers via gRPC.

Example:
  # Start agent with Docker and Mock providers (insecure, for testing)
  boxy agent serve --listen :50051 --providers docker,mock

  # Start agent with mTLS (production)
  boxy agent serve --listen :50051 \
    --providers hyperv \
    --tls-cert /path/to/agent.crt \
    --tls-key /path/to/agent.key \
    --tls-ca /path/to/ca.crt \
    --use-tls

  # Windows agent with Hyper-V
  boxy agent serve --listen :50051 --providers hyperv --use-tls

The agent will automatically detect and register available providers based on
the current platform. Use --providers to explicitly specify which to enable.`,
	RunE: runAgentServe,
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(agentServeCmd)

	agentServeCmd.Flags().StringVar(&agentListenAddr, "listen", ":50051", "Address to listen on (host:port)")
	agentServeCmd.Flags().StringVar(&agentTLSCert, "tls-cert", "", "Path to TLS certificate")
	agentServeCmd.Flags().StringVar(&agentTLSKey, "tls-key", "", "Path to TLS private key")
	agentServeCmd.Flags().StringVar(&agentTLSCA, "tls-ca", "", "Path to CA certificate")
	agentServeCmd.Flags().BoolVar(&agentUseTLS, "use-tls", false, "Enable TLS (requires --tls-cert, --tls-key, --tls-ca)")
	agentServeCmd.Flags().StringSliceVar(&agentProviders, "providers", []string{}, "Providers to enable (docker,hyperv,mock,scratch/shell)")
}

func runAgentServe(cmd *cobra.Command, args []string) error {
	logger := logrus.New()
	level, _ := logrus.ParseLevel(logLevel)
	logger.SetLevel(level)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	logger.WithFields(logrus.Fields{
		"version": "vdev",
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}).Info("Starting Boxy Agent")

	// Generate agent ID (in production, this would come from certificate CN)
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname: %w", err)
	}
	agentID := fmt.Sprintf("%s-%d", hostname, time.Now().Unix())

	// Create agent server
	cfg := &agent.Config{
		AgentID:     agentID,
		ListenAddr:  agentListenAddr,
		TLSCertPath: agentTLSCert,
		TLSKeyPath:  agentTLSKey,
		TLSCAPath:   agentTLSCA,
		UseTLS:      agentUseTLS,
	}

	if agentUseTLS && (agentTLSCert == "" || agentTLSKey == "" || agentTLSCA == "") {
		return fmt.Errorf("--use-tls requires --tls-cert, --tls-key, and --tls-ca")
	}

	srv, err := agent.NewServer(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create agent server: %w", err)
	}

	// Register providers
	if err := registerProviders(srv, logger); err != nil {
		return fmt.Errorf("failed to register providers: %w", err)
	}

	// Start server in background
	errChan := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			errChan <- err
		}
	}()

	logger.WithFields(logrus.Fields{
		"agent_id": agentID,
		"address":  agentListenAddr,
		"tls":      agentUseTLS,
	}).Info("Agent server started successfully")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return fmt.Errorf("agent server error: %w", err)
	case sig := <-sigChan:
		logger.WithField("signal", sig).Info("Received shutdown signal")
		srv.Stop()
		logger.Info("Agent server stopped")
	}

	return nil
}

// registerProviders registers providers based on flags and platform
//
// **Potential Problem #8 Addressed: Platform Detection**
// - Automatically detects available providers based on OS
// - Allows manual override via --providers flag
func registerProviders(srv *agent.Server, logger *logrus.Logger) error {
	// Determine which providers to enable
	providersToEnable := agentProviders
	if len(providersToEnable) == 0 {
		// Auto-detect based on platform
		switch runtime.GOOS {
		case "windows":
			providersToEnable = []string{"hyperv"}
			logger.Info("Auto-detected Windows platform, enabling Hyper-V provider")
		case "linux":
			providersToEnable = []string{"docker"}
			logger.Info("Auto-detected Linux platform, enabling Docker provider")
		default:
			return fmt.Errorf("unsupported platform: %s (use --providers to manually specify)", runtime.GOOS)
		}
	}

	// Create encryption key (in production, this should be loaded from secure storage)
	encryptionKey := []byte("01234567890123456789012345678901") // 32 bytes for AES-256
	encryptor, err := crypto.NewEncryptor(encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to create encryptor: %w", err)
	}

	// Register each provider
	for _, provName := range providersToEnable {
		switch provName {
		case "docker":
			prov, err := docker.NewProvider(logger, encryptor)
			if err != nil {
				logger.WithError(err).Warn("Failed to create Docker provider, skipping")
				continue
			}
			if err := srv.RegisterProvider("docker", prov); err != nil {
				return err
			}
			logger.Info("Registered Docker provider")

		case "hyperv":
			if runtime.GOOS != "windows" {
				logger.Warn("Hyper-V provider requires Windows, skipping")
				continue
			}
			prov := hyperv.NewProvider(logger, encryptor)
			if err := srv.RegisterProvider("hyperv", prov); err != nil {
				return err
			}
			logger.Info("Registered Hyper-V provider")

		case "mock":
			mockCfg := &mock.Config{
				ProvisionDelay: 2 * time.Second,
				DestroyDelay:   1 * time.Second,
			}
			prov := mock.NewProvider(logger, mockCfg)
			if err := srv.RegisterProvider("mock", prov); err != nil {
				return err
			}
			logger.Info("Registered Mock provider")

		case "scratch/shell":
			prov := shell.New(logger, shell.Config{})
			if err := srv.RegisterProvider(prov.Name(), prov); err != nil {
				return err
			}
			logger.Info("Registered scratch/shell provider")

		default:
			logger.WithField("provider", provName).Warn("Unknown provider, skipping")
		}
	}

	return nil
}

// agentStatusCmd checks agent status
var agentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check agent status",
	Long: `Check the status of a running Boxy agent.

Example:
  boxy agent status --agent-id my-agent
  boxy agent status --agent-addr localhost:50051`,
	RunE: runAgentStatus,
}

func init() {
	agentCmd.AddCommand(agentStatusCmd)
}

func runAgentStatus(cmd *cobra.Command, args []string) error {
	// TODO: Implement agent status check
	// This would connect to the agent and call health check
	fmt.Println("Agent status check not yet implemented")
	fmt.Println("Future: This will connect to agent and check health of all providers")
	return nil
}
