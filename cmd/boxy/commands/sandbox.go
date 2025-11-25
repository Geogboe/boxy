package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/runtime"
	"github.com/Geogboe/boxy/pkg/crypto"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		poolReqs, err := cmd.Flags().GetStringArray("pool")
		if err != nil {
			return err
		}
		if len(poolReqs) == 0 {
			return fmt.Errorf("at least one --pool flag required (format: pool-name:count)")
		}

		resources, err := parsePoolRequests(poolReqs)
		if err != nil {
			return err
		}

		duration, err := time.ParseDuration(sandboxDuration)
		if err != nil {
			return fmt.Errorf("invalid duration: %w", err)
		}

		// Storage
		store, err := runtime.OpenStorage(cfg)
		if err != nil {
			return err
		}
		defer store.Close()

		// Encryption
		encryptionKey, err := config.GetEncryptionKey()
		if err != nil {
			return fmt.Errorf("failed to get encryption key: %w", err)
		}
		encryptor, err := crypto.NewEncryptor(encryptionKey)
		if err != nil {
			return fmt.Errorf("failed to create encryptor: %w", err)
		}

		ctx := context.Background()
		registry := runtime.BuildRegistry(ctx, cfg.Pools, logger, encryptor)
		rt, err := runtime.StartSandboxRuntime(ctx, cfg, registry, store, logger)
		if err != nil {
			return err
		}
		defer rt.Stop(logger)

		fmt.Println("Creating sandbox...")
		createReq := runtime.CreateSandboxRequest{
			Name:      sandboxName,
			Resources: resources,
			Duration:  duration,
		}

		sb, err := runtime.CreateSandbox(ctx, rt, createReq)
		if err != nil {
			return fmt.Errorf("failed to create sandbox: %w", err)
		}

		fmt.Printf("✓ Sandbox %s created (allocating resources...)\n", sb.ID[:8])
		fmt.Print("Waiting for resources")
		sb, err = runtime.WaitSandboxReady(ctx, rt, sb.ID, 5*time.Minute)
		if err != nil {
			fmt.Println(" ✗")
			return fmt.Errorf("failed to allocate resources: %w", err)
		}
		fmt.Println(" ✓")

		if sandboxJSON {
			data, _ := json.MarshalIndent(sb, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		printSandboxSummary(sb)
		resourcesWithConn, err := runtime.GetSandboxResources(ctx, rt, sb.ID)
		if err != nil {
			logger.WithError(err).Warn("Failed to get connection info")
			return nil
		}
		printConnections(resourcesWithConn)
		return nil
	},
}

var sandboxListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sandboxes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		store, err := runtime.OpenStorage(cfg)
		if err != nil {
			return err
		}
		defer store.Close()

		sbList, err := runtime.ListSandboxes(context.Background(), store)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tName\tState\tExpires")
		for _, sb := range sbList {
			exp := ""
			if sb.ExpiresAt != nil {
				exp = sb.ExpiresAt.Format(time.RFC3339)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", sb.ID, sb.Name, sb.State, exp)
		}
		w.Flush()
		return nil
	},
}

var sandboxDestroyCmd = &cobra.Command{
	Use:   "destroy [sandboxID]",
	Short: "Destroy a sandbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID := args[0]
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		store, err := runtime.OpenStorage(cfg)
		if err != nil {
			return err
		}
		defer store.Close()

		ctx := createSignalContext()
		if err := runtime.DestroySandbox(ctx, store, sandboxID); err != nil {
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

func parsePoolRequests(reqs []string) ([]runtime.ResourceRequest, error) {
	var out []runtime.ResourceRequest
	for _, req := range reqs {
		parts := strings.Split(req, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid pool format: %s (expected: pool-name:count)", req)
		}
		poolName := parts[0]
		count, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid count in pool format: %s (expected: pool-name:count)", req)
		}
		out = append(out, runtime.ResourceRequest{
			PoolName: poolName,
			Count:    count,
		})
	}
	return out, nil
}

func printSandboxSummary(sb runtime.SandboxView) {
	fmt.Printf("\n✓ Sandbox ready\n\n")
	fmt.Printf("ID:         %s\n", sb.ID)
	if sb.Name != "" {
		fmt.Printf("Name:       %s\n", sb.Name)
	}
	fmt.Printf("Resources:  %d\n", len(sb.ResourceIDs))
	fmt.Printf("State:      %s\n", sb.State)
	if sb.ExpiresAt != nil {
		fmt.Printf("Expires:    %s (in %s)\n", sb.ExpiresAt.Format(time.RFC3339), sb.TimeRemaining().Round(time.Second))
	}
	fmt.Println()
}

func printConnections(resources []runtime.ResourceWithConn) {
	fmt.Println("Resource Connection Info:")
	fmt.Println("─────────────────────────")
	for i, rwc := range resources {
		fmt.Printf("\n[%d] Resource %s\n", i+1, rwc.Resource.ID[:8])
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
		if cs, ok := rwc.Connection.ExtraFields["connect_script"].(string); ok && cs != "" {
			fmt.Printf("    Connect script: %s\n", cs)
		}
		if ws, ok := rwc.Connection.ExtraFields["workspace_dir"].(string); ok && ws != "" {
			fmt.Printf("    Workspace dir: %s\n", ws)
		}
	}
}
