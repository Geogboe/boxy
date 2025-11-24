package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	boxyruntime "github.com/Geogboe/boxy/internal/runtime"
	"github.com/Geogboe/boxy/pkg/agent"
	"github.com/Geogboe/boxy/pkg/crypto"
)

var (
	agentListenAddr string
	agentTLSCert    string
	agentTLSKey     string
	agentTLSCA      string
	agentUseTLS     bool
	agentProviders  []string
	agentKeyBase64  string
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Agent commands for distributed resource management",
	Long: `Manage Boxy agents that run on remote machines and provide access to local resources.

Agents run on Windows machines (for Hyper-V), Linux machines (for KVM, Docker), or any
host that has virtualization/containerization resources. They expose local providers
via gRPC so the central Boxy service can provision resources remotely.`,
}

var agentServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a Boxy agent server",
	RunE:  runAgentServe,
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
	agentServeCmd.Flags().StringVar(&agentKeyBase64, "encryption-key", "", "Base64-encoded 32-byte key for agent provider encryption (falls back to BOXY_ENCRYPTION_KEY or generates ephemeral)")
}

func runAgentServe(cmd *cobra.Command, args []string) error {
	logger := logrus.New()
	level, _ := logrus.ParseLevel(logLevel)
	logger.SetLevel(level)
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	logger.WithFields(logrus.Fields{
		"version": "vdev",
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}).Info("Starting Boxy Agent")

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname: %w", err)
	}
	agentID := fmt.Sprintf("%s-%d", hostname, time.Now().Unix())

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

	providersToEnable := agentProviders
	if len(providersToEnable) == 0 {
		switch runtime.GOOS {
		case "windows":
			providersToEnable = []string{"hyperv"}
		case "linux":
			providersToEnable = []string{"docker"}
		default:
			return fmt.Errorf("unsupported platform: %s (use --providers to manually specify)", runtime.GOOS)
		}
	}

	keyBytes, err := loadAgentKey()
	if err != nil {
		return err
	}

	encryptor, err := crypto.NewEncryptor(keyBytes)
	if err != nil {
		return fmt.Errorf("failed to create encryptor: %w", err)
	}

	boxyruntime.AgentBootstrap(context.Background(), srv, logger, encryptor, providersToEnable)

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

func loadAgentKey() ([]byte, error) {
	if agentKeyBase64 != "" {
		b, err := base64.StdEncoding.DecodeString(agentKeyBase64)
		if err != nil {
			return nil, fmt.Errorf("invalid --encryption-key (base64 decode): %w", err)
		}
		if len(b) != 32 {
			return nil, fmt.Errorf("--encryption-key must be 32 bytes after base64 decoding (got %d)", len(b))
		}
		return b, nil
	}
	if env := os.Getenv("BOXY_ENCRYPTION_KEY"); env != "" {
		b, err := base64.StdEncoding.DecodeString(env)
		if err == nil && len(b) == 32 {
			return b, nil
		}
	}
	// Last resort: generate ephemeral key (sufficient for local/insecure agent use)
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}
	return key, nil
}
