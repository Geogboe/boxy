package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/core/pool"
	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/internal/provider/docker"
	"github.com/Geogboe/boxy/internal/storage"
	"github.com/Geogboe/boxy/pkg/provider"
)

var (
	sandboxName     string
	sandboxDuration string
	sandboxJSON     bool
)

var sandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Manage sandboxes",
	Long:  `Create, list, and destroy sandboxes.`,
}

var sandboxCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new sandbox",
	Long: `Create a new sandbox by allocating resources from pools.

Example:
  boxy sandbox create --pool ubuntu-containers:2 --pool nginx-containers:1 --duration 2h --name my-lab`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// Parse pool requests
		poolReqs, err := cmd.Flags().GetStringArray("pool")
		if err != nil {
			return err
		}

		if len(poolReqs) == 0 {
			return fmt.Errorf("at least one --pool flag required")
		}

		var resources []sandbox.ResourceRequest
		for _, req := range poolReqs {
			var poolName string
			var count int
			_, err := fmt.Sscanf(req, "%[^:]:%d", &poolName, &count)
			if err != nil {
				return fmt.Errorf("invalid pool format: %s (expected: pool-name:count)", req)
			}

			resources = append(resources, sandbox.ResourceRequest{
				PoolName: poolName,
				Count:    count,
			})
		}

		// Parse duration
		duration, err := time.ParseDuration(sandboxDuration)
		if err != nil {
			return fmt.Errorf("invalid duration: %w", err)
		}

		// Initialize dependencies
		store, err := storage.NewSQLiteStore(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("failed to initialize storage: %w", err)
		}
		defer store.Close()

		dockerProvider, err := docker.NewProvider(logger)
		if err != nil {
			return fmt.Errorf("failed to create Docker provider: %w", err)
		}

		providerRegistry := provider.NewRegistry()
		providerRegistry.Register("docker", dockerProvider)

		resourceRepo := storage.NewResourceRepositoryAdapter(store)

		// Create pool allocators
		poolAllocators := make(map[string]sandbox.PoolAllocator)
		for _, poolCfg := range cfg.Pools {
			prov, ok := providerRegistry.Get(poolCfg.Backend)
			if !ok {
				continue
			}

			manager, err := pool.NewManager(&poolCfg, prov, resourceRepo, logger)
			if err != nil {
				return fmt.Errorf("failed to create pool manager for %s: %w", poolCfg.Name, err)
			}

			poolAllocators[poolCfg.Name] = manager
		}

		// Create sandbox manager
		sandboxMgr := sandbox.NewManager(
			poolAllocators,
			store,
			store,
			providerRegistry,
			logger,
		)

		// Create sandbox
		fmt.Println("Creating sandbox...")

		createReq := &sandbox.CreateRequest{
			Name:      sandboxName,
			Resources: resources,
			Duration:  duration,
		}

		sb, err := sandboxMgr.Create(context.Background(), createReq)
		if err != nil {
			return fmt.Errorf("failed to create sandbox: %w", err)
		}

		if sandboxJSON {
			data, _ := json.MarshalIndent(sb, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("\n✓ Sandbox created successfully\n\n")
			fmt.Printf("ID:         %s\n", sb.ID)
			if sb.Name != "" {
				fmt.Printf("Name:       %s\n", sb.Name)
			}
			fmt.Printf("Resources:  %d\n", len(sb.ResourceIDs))
			fmt.Printf("Expires:    %s (in %s)\n", sb.ExpiresAt.Format(time.RFC3339), sb.TimeRemaining().Round(time.Second))
			fmt.Println()

			// Show connection info
			fmt.Println("Resource Connection Info:")
			fmt.Println("─────────────────────────")

			resourcesWithConn, err := sandboxMgr.GetResourcesForSandbox(context.Background(), sb.ID)
			if err != nil {
				logger.WithError(err).Warn("Failed to get connection info")
			} else {
				for i, rwc := range resourcesWithConn {
					fmt.Printf("\n[%d] Resource %s\n", i+1, rwc.Resource.ID)
					fmt.Printf("    Type: %s\n", rwc.Connection.Type)
					if rwc.Connection.Host != "" {
						fmt.Printf("    Host: %s\n", rwc.Connection.Host)
					}
					if rwc.Connection.Username != "" {
						fmt.Printf("    Username: %s\n", rwc.Connection.Username)
					}
					if rwc.Connection.Password != "" {
						fmt.Printf("    Password: %s\n", rwc.Connection.Password)
					}
					if containerID, ok := rwc.Connection.ExtraFields["container_id"].(string); ok {
						fmt.Printf("    Container: %s\n", containerID)
						fmt.Printf("    Connect: docker exec -it %s /bin/bash\n", containerID[:12])
					}
				}
			}

			fmt.Println()
			fmt.Printf("To destroy: boxy sandbox destroy %s\n", sb.ID)
			fmt.Println()
		}

		return nil
	},
}

var sandboxListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all sandboxes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		store, err := storage.NewSQLiteStore(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("failed to initialize storage: %w", err)
		}
		defer store.Close()

		sandboxes, err := store.ListActiveSandboxes(context.Background())
		if err != nil {
			return fmt.Errorf("failed to list sandboxes: %w", err)
		}

		if len(sandboxes) == 0 {
			fmt.Println("\nNo active sandboxes")
			return nil
		}

		fmt.Println("\nActive Sandboxes:")
		fmt.Println()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tRESOURCES\tCREATED\tEXPIRES\tTIME REMAINING")
		fmt.Fprintln(w, "--\t----\t---------\t-------\t-------\t--------------")

		for _, sb := range sandboxes {
			name := sb.Name
			if name == "" {
				name = "-"
			}

			timeRemaining := "-"
			expiresAt := "-"
			if sb.ExpiresAt != nil {
				expiresAt = sb.ExpiresAt.Format("15:04:05")
				remaining := sb.TimeRemaining()
				if remaining > 0 {
					timeRemaining = remaining.Round(time.Second).String()
				} else {
					timeRemaining = "EXPIRED"
				}
			}

			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
				sb.ID[:8],
				name,
				len(sb.ResourceIDs),
				sb.CreatedAt.Format("15:04:05"),
				expiresAt,
				timeRemaining,
			)
		}

		w.Flush()
		fmt.Println()

		return nil
	},
}

var sandboxDestroyCmd = &cobra.Command{
	Use:   "destroy <sandbox-id>",
	Short: "Destroy a sandbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID := args[0]

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		store, err := storage.NewSQLiteStore(cfg.Storage.Path)
		if err != nil {
			return fmt.Errorf("failed to initialize storage: %w", err)
		}
		defer store.Close()

		dockerProvider, err := docker.NewProvider(logger)
		if err != nil {
			return fmt.Errorf("failed to create Docker provider: %w", err)
		}

		providerRegistry := provider.NewRegistry()
		providerRegistry.Register("docker", dockerProvider)

		resourceRepo := storage.NewResourceRepositoryAdapter(store)

		// Create pool allocators
		poolAllocators := make(map[string]sandbox.PoolAllocator)
		for _, poolCfg := range cfg.Pools {
			prov, ok := providerRegistry.Get(poolCfg.Backend)
			if !ok {
				continue
			}

			manager, err := pool.NewManager(&poolCfg, prov, resourceRepo, logger)
			if err != nil {
				return fmt.Errorf("failed to create pool manager for %s: %w", poolCfg.Name, err)
			}

			poolAllocators[poolCfg.Name] = manager
		}

		sandboxMgr := sandbox.NewManager(
			poolAllocators,
			store,
			store,
			providerRegistry,
			logger,
		)

		fmt.Printf("Destroying sandbox %s...\n", sandboxID)

		if err := sandboxMgr.Destroy(context.Background(), sandboxID); err != nil {
			return fmt.Errorf("failed to destroy sandbox: %w", err)
		}

		fmt.Printf("✓ Sandbox destroyed\n")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(sandboxCmd)

	sandboxCmd.AddCommand(sandboxCreateCmd)
	sandboxCreateCmd.Flags().StringArrayP("pool", "p", []string{}, "Pool allocation (format: pool-name:count)")
	sandboxCreateCmd.Flags().StringVarP(&sandboxName, "name", "n", "", "Sandbox name")
	sandboxCreateCmd.Flags().StringVarP(&sandboxDuration, "duration", "d", "2h", "Sandbox duration (e.g., 2h, 30m)")
	sandboxCreateCmd.Flags().BoolVar(&sandboxJSON, "json", false, "Output JSON")

	sandboxCmd.AddCommand(sandboxListCmd)
	sandboxCmd.AddCommand(sandboxDestroyCmd)
}
