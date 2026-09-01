package cli

import (
	"fmt"

	"github.com/Geogboe/boxy/internal/pool"
	"github.com/spf13/cobra"
)

func newDebugResourceCommand() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "resource",
		Short: "Run resource maintenance actions through the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&server, "server", "", "server address (overrides BOXY_SERVER and the global client default)")
	cmd.AddCommand(newDebugResourcePurgeCommand(func() string { return server }))
	return cmd
}

func newDebugResourcePurgeCommand(serverAddr func() string) *cobra.Command {
	var dryRun bool
	var force bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Preview or force cleanup of stale resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && force {
				return fmt.Errorf("--dry-run and --force cannot be combined")
			}
			if !force {
				dryRun = true
			}
			report, err := postJSON[map[string]any, pool.CleanupReport](
				cmd.Context(),
				maintenanceAPIClientForServer(serverAddr()),
				apiBaseURL(serverAddr())+"/api/v1/resources/purge",
				map[string]any{"dry_run": dryRun, "force": force},
			)
			if err != nil {
				return fmt.Errorf("purge resources: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "resource purge: candidates=%d cleaned=%d skipped=%d errors=%d dry-run=%t force=%t\n",
				report.CandidateCount, len(report.CleanedIDs), len(report.SkippedIDs), len(report.Errors), report.DryRun, report.Force)
			for _, item := range report.Errors {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "error %s: %s\n", item.ID, item.Error)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview eligible resources without changing state")
	cmd.Flags().BoolVar(&force, "force", false, "confirm cleanup and retry eligible provider resources")
	return cmd
}
