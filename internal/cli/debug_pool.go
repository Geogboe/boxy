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
	cmd.PersistentFlags().StringVar(&server, "server", "", "server address (default 127.0.0.1:9090)")
	serverAddr := func() string { return server }
	cmd.AddCommand(newDebugPoolDrainCommand(serverAddr))
	cmd.AddCommand(newDebugPoolFillCommand(serverAddr))
	return cmd
}

func newDebugPoolDrainCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "drain <pool>",
		Short: "Drain unused ready inventory from a pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := postJSON[map[string]any, model.Pool](
				cmd.Context(),
				maintenanceAPIClient(),
				apiBaseURL(serverAddr())+"/api/v1/pools/"+args[0]+"/drain",
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

func newDebugPoolFillCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "fill <pool>",
		Short: "Fill a pool to its configured min_ready",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := postJSON[map[string]any, model.Pool](
				cmd.Context(),
				maintenanceAPIClient(),
				apiBaseURL(serverAddr())+"/api/v1/pools/"+args[0]+"/fill",
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
