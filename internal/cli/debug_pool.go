package cli

import (
	"fmt"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/spf13/cobra"
)

func newDebugPoolCommand() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Run pool maintenance actions through the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&server, "server", "", "server address (overrides BOXY_SERVER and the global client default)")
	serverAddr := func() string { return server }
	cmd.AddCommand(newPoolDrainCommand(serverAddr))
	cmd.AddCommand(newPoolFillCommand(serverAddr))
	return cmd
}

func newPoolListCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured pools and ready inventory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pools, err := fetchJSON[[]model.Pool](cmd.Context(), maintenanceAPIClientForServer(serverAddr()), apiBaseURL(serverAddr())+"/api/v1/pools")
			if err != nil {
				return fmt.Errorf("list pools: %w", err)
			}
			for _, pool := range pools {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%d ready\n", pool.Name, len(pool.Inventory.Resources))
			}
			return nil
		},
	}
}

func newAdminPoolCommand(serverAddr func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Run administrator pool maintenance actions through the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPoolListCommand(serverAddr))
	cmd.AddCommand(newPoolDrainCommand(serverAddr))
	cmd.AddCommand(newPoolFillCommand(serverAddr))
	return cmd
}

func newPoolDrainCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "drain <pool>",
		Short: "Drain unused ready inventory from a pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := validatePathID("pool name", args[0])
			if err != nil {
				return err
			}
			pool, err := postJSON[map[string]any, model.Pool](
				cmd.Context(),
				maintenanceAPIClientForServer(serverAddr()),
				apiBaseURL(serverAddr())+"/api/v1/pools/"+name+"/drain",
				map[string]any{},
			)
			if err != nil {
				return fmt.Errorf("drain pool %q: %w", args[0], err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "drained pool %s\n", pool.Name)
			return nil
		},
	}
}

func newPoolFillCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "fill <pool>",
		Short: "Fill a pool to its configured min_ready",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := validatePathID("pool name", args[0])
			if err != nil {
				return err
			}
			pool, err := postJSON[map[string]any, model.Pool](
				cmd.Context(),
				maintenanceAPIClientForServer(serverAddr()),
				apiBaseURL(serverAddr())+"/api/v1/pools/"+name+"/fill",
				map[string]any{},
			)
			if err != nil {
				return fmt.Errorf("fill pool %q: %w", args[0], err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "filled pool %s\n", pool.Name)
			return nil
		},
	}
}
