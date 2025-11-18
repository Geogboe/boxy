package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/internal/crypto"
	"github.com/Geogboe/boxy/internal/provider/docker"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/provider"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Boxy service",
	Long: `Starts the Boxy service with warm pool maintenance and automatic cleanup.

The service will:
- Start all configured pools with warm pool maintenance
- Maintain min_ready count for each pool
- Automatically clean up expired sandboxes
- Perform health checks on resources`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load configuration
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		logger.Info("Starting Boxy service")

		// Initialize storage
		store, err := storage.NewSQLiteStore(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("failed to initialize storage: %w", err)
		}
		defer store.Close()

		logger.WithField("db_path", cfg.Storage.Path).Info("Storage initialized")

		// Initialize encryption
		encryptionKey, err := config.GetEncryptionKey()
		if err != nil {
			return fmt.Errorf("failed to get encryption key: %w", err)
		}
		encryptor, err := crypto.NewEncryptor(encryptionKey)
		if err != nil {
			return fmt.Errorf("failed to create encryptor: %w", err)
		}
		logger.Info("Encryption initialized")

		// Initialize provider registry
		providerRegistry := provider.NewRegistry()

		// Register Docker provider
		dockerProvider, err := docker.NewProvider(logger, encryptor)
		if err != nil {
			return fmt.Errorf("failed to create Docker provider: %w", err)
		}
		providerRegistry.Register("docker", dockerProvider)
		logger.Info("Docker provider registered")

		// Health check Docker
		if err := dockerProvider.HealthCheck(context.Background()); err != nil {
			logger.WithError(err).Warn("Docker health check failed - Docker functionality may be limited")
		} else {
			logger.Info("Docker daemon is healthy")
		}

		// Create resource repository adapter
		resourceRepo := storage.NewResourceRepositoryAdapter(store)

		// Initialize pool managers
		poolManagers := make(map[string]*pool.Manager)
		poolAllocators := make(map[string]sandbox.PoolAllocator)

		for _, poolCfg := range cfg.Pools {
			// Get provider for this pool
			prov, ok := providerRegistry.Get(poolCfg.Backend)
			if !ok {
				logger.WithField("backend", poolCfg.Backend).Error("Provider not found, skipping pool")
				continue
			}

			// Create pool manager
			manager, err := pool.NewManager(&poolCfg, prov, resourceRepo, logger)
			if err != nil {
				return fmt.Errorf("failed to create pool manager for %s: %w", poolCfg.Name, err)
			}

			// Start pool manager
			if err := manager.Start(); err != nil {
				return fmt.Errorf("failed to start pool manager for %s: %w", poolCfg.Name, err)
			}

			poolManagers[poolCfg.Name] = manager
			poolAllocators[poolCfg.Name] = manager

			logger.WithField("pool", poolCfg.Name).Info("Pool manager started")
		}

		if len(poolManagers) == 0 {
			return fmt.Errorf("no pools configured or all pools failed to start")
		}

		// Initialize sandbox manager
		sandboxMgr := sandbox.NewManager(
			poolAllocators,
			store,
			store,
			providerRegistry,
			logger,
		)
		sandboxMgr.Start()

		logger.Info("✓ Boxy service started successfully")
		fmt.Printf("\n")
		fmt.Printf("✓ Boxy service is running\n")
		fmt.Printf("  • %d pools active\n", len(poolManagers))
		fmt.Printf("  • Database: %s\n", cfg.Storage.Path)
		fmt.Printf("\nPress Ctrl+C to stop\n\n")

		// Wait for interrupt signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		logger.Info("Shutting down Boxy service...")
		fmt.Printf("\nShutting down gracefully...\n")

		// Stop sandbox manager
		sandboxMgr.Stop()

		// Stop all pool managers
		for name, mgr := range poolManagers {
			logger.WithField("pool", name).Info("Stopping pool manager")
			if err := mgr.Stop(); err != nil {
				logger.WithError(err).WithField("pool", name).Error("Error stopping pool manager")
			}
		}

		logger.Info("Boxy service stopped")
		fmt.Printf("✓ Service stopped\n")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
