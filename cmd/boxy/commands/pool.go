package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/crypto"
	"github.com/Geogboe/boxy/pkg/provider/docker"
)

var poolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Manage resource pools",
	Long:  `View and manage resource pools.`,
}

var poolListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all pools and their status",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// Initialize storage
		store, err := storage.NewSQLiteStore(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("failed to initialize storage: %w", err)
		}
		defer func() {
			if err := store.Close(); err != nil {
				logger.WithError(err).Error("Failed to close storage")
			}
		}()

		// Initialize encryption
		encryptionKey, err := config.GetEncryptionKey()
		if err != nil {
			return fmt.Errorf("failed to get encryption key: %w", err)
		}
		encryptor, err := crypto.NewEncryptor(encryptionKey)
		if err != nil {
			return fmt.Errorf("failed to create encryptor: %w", err)
		}

		// Initialize Docker provider
		dockerProvider, err := docker.NewProvider(logger, encryptor)
		if err != nil {
			return fmt.Errorf("failed to create Docker provider: %w", err)
		}

		resourceRepo := storage.NewResourceRepositoryAdapter(store)

		fmt.Println("\nResource Pools:")
		fmt.Println()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tBACKEND\tIMAGE\tREADY\tALLOCATED\tMIN\tMAX\tHEALTHY")
		fmt.Fprintln(w, "----\t----\t-------\t-----\t-----\t---------\t---\t---\t-------")

		for _, poolCfg := range cfg.Pools {
			// Create temporary manager to get stats
			manager, err := pool.NewManager(&poolCfg, dockerProvider, resourceRepo, logger)
			if err != nil {
				logger.WithError(err).WithField("pool", poolCfg.Name).Error("Failed to create pool manager")
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stats, err := manager.GetStats(ctx)
			if err != nil {
				logger.WithError(err).WithField("pool", poolCfg.Name).Error("Failed to get pool stats")
				continue
			}

			healthy := "✓"
			if !stats.Healthy {
				healthy = "✗"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
				stats.Name,
				poolCfg.Type,
				poolCfg.Backend,
				poolCfg.Image,
				stats.TotalReady,
				stats.TotalAllocated,
				stats.MinReady,
				stats.MaxTotal,
				healthy,
			)
		}

		if err := w.Flush(); err != nil {
			return fmt.Errorf("failed to flush output: %w", err)
		}
		fmt.Println()

		return nil
	},
}

var poolStatsCmd = &cobra.Command{
	Use:   "stats <pool-name>",
	Short: "Show detailed statistics for a pool",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		poolName := args[0]

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// Find pool config
		var poolCfg *pool.PoolConfig
		for _, pc := range cfg.Pools {
			if pc.Name == poolName {
				poolCfg = &pc
				break
			}
		}

		if poolCfg == nil {
			return fmt.Errorf("pool not found: %s", poolName)
		}

		// Initialize storage
		store, err := storage.NewSQLiteStore(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("failed to initialize storage: %w", err)
		}
		defer func() {
			if err := store.Close(); err != nil {
				logger.WithError(err).Error("Failed to close storage")
			}
		}()

		// Initialize encryption
		encryptionKey, err := config.GetEncryptionKey()
		if err != nil {
			return fmt.Errorf("failed to get encryption key: %w", err)
		}
		encryptor, err := crypto.NewEncryptor(encryptionKey)
		if err != nil {
			return fmt.Errorf("failed to create encryptor: %w", err)
		}

		// Initialize Docker provider
		dockerProvider, err := docker.NewProvider(logger, encryptor)
		if err != nil {
			return fmt.Errorf("failed to create Docker provider: %w", err)
		}

		resourceRepo := storage.NewResourceRepositoryAdapter(store)

		// Create manager
		manager, err := pool.NewManager(poolCfg, dockerProvider, resourceRepo, logger)
		if err != nil {
			return fmt.Errorf("failed to create pool manager: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stats, err := manager.GetStats(ctx)
		if err != nil {
			return fmt.Errorf("failed to get pool stats: %w", err)
		}

		fmt.Printf("\nPool: %s\n", stats.Name)
		fmt.Println("─────────────────────────────")
		fmt.Printf("Ready:         %d\n", stats.TotalReady)
		fmt.Printf("Allocated:     %d\n", stats.TotalAllocated)
		fmt.Printf("Provisioning:  %d\n", stats.TotalProvisioning)
		fmt.Printf("Error:         %d\n", stats.TotalError)
		fmt.Printf("Total:         %d\n", stats.Total)
		fmt.Println()
		fmt.Printf("Min Ready:     %d\n", stats.MinReady)
		fmt.Printf("Max Total:     %d\n", stats.MaxTotal)
		fmt.Printf("Healthy:       %v\n", stats.Healthy)
		fmt.Println()

		return nil
	},
}

func init() {
	rootCmd.AddCommand(poolCmd)
	poolCmd.AddCommand(poolListCmd)
	poolCmd.AddCommand(poolStatsCmd)
}
