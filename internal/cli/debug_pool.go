package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/pkg/jobs"
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
		Short: "List configured pools, ready inventory, and quarantined resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := maintenanceAPIClientForServer(serverAddr())
			base := apiBaseURL(serverAddr())
			pools, err := fetchJSON[[]model.Pool](cmd.Context(), client, base+"/api/v1/pools")
			if err != nil {
				return fmt.Errorf("list pools: %w", err)
			}
			// A pool's own Inventory.Resources only ever holds Ready
			// resources (RebuildReadyInventory re-admits Ready state only,
			// see internal/pool/manager.go) -- it never reflects
			// quarantined (ResourceStateError) resources, which is exactly
			// the #328 gap: "0 ready" alone is indistinguishable from a
			// converged idle pool. Cross-reference the full resource list to
			// count quarantined resources per pool instead. Best-effort: this
			// is a secondary signal on top of the pool listing itself, and
			// `list` is exactly the command an operator reaches for when a
			// pool is already wedged -- it must stay available even if the
			// resource listing call fails for some unrelated reason, just
			// without a quarantined count.
			quarantinedByPool := make(map[model.PoolName]int, len(pools))
			if resources, rerr := fetchJSON[[]model.Resource](cmd.Context(), client, base+"/api/v1/resources"); rerr == nil {
				for _, res := range resources {
					if res.State == model.ResourceStateError {
						quarantinedByPool[res.EffectivePool()]++
					}
				}
			}
			for _, pool := range pools {
				quarantined := quarantinedByPool[pool.Name]
				maxTotal := "-"
				if pool.Policies.Preheat.MaxTotal > 0 {
					maxTotal = fmt.Sprintf("%d", pool.Policies.Preheat.MaxTotal)
				}
				line := fmt.Sprintf("%s\t%d ready\t%d quarantined\tmax_total=%s", pool.Name, len(pool.Inventory.Resources), quarantined, maxTotal)
				if quarantined > 0 && pool.Policies.Preheat.MaxTotal > 0 && quarantined >= pool.Policies.Preheat.MaxTotal {
					line += "\tBLOCKED (quarantined resources consume max_total)"
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
}

func newAdminPoolCommand(serverAddr func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pool",
		Aliases: []string{"pools"},
		Short:   "Run administrator pool maintenance actions through the daemon",
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
		Use:     "drain <pool>",
		Aliases: []string{"down"},
		Short:   "Drain unused ready inventory from a pool",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := validatePathID("pool name", args[0])
			if err != nil {
				return err
			}
			return runPoolMaintenanceJob(cmd, serverAddr(), "drain", "drained pool %s\n", "/api/v1/pools/"+name+"/drain", args[0])
		},
	}
}

func newPoolFillCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:     "fill <pool>",
		Aliases: []string{"up"},
		Short:   "Fill a pool to its configured min_ready",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := validatePathID("pool name", args[0])
			if err != nil {
				return err
			}
			return runPoolMaintenanceJob(cmd, serverAddr(), "fill", "filled pool %s\n", "/api/v1/pools/"+name+"/fill", args[0])
		},
	}
}

// runPoolMaintenanceJob drives a POST .../drain or .../fill call through to
// completion. As of #328's fix, the server always answers 202 Accepted with
// a durable jobs.Job (see internal/server/api_pools.go's
// submitPoolMaintenanceJob) that runs asynchronously -- there is no
// synchronous model.Pool response to decode. Before this, both commands
// decoded the 202 body straight into a model.Pool, which silently produced
// a zero-value Pool (no matching JSON fields) and printed success
// regardless of what actually happened; polling the job to a terminal
// state and inspecting its ErrorCode is what makes the quarantine-exhausted
// case (and any other job failure) visible instead of a false "filled"/
// "drained" success. successMsg must contain exactly one %s for poolName.
func runPoolMaintenanceJob(cmd *cobra.Command, server, action, successMsg, path, poolName string) error {
	client := maintenanceAPIClientForServer(server)
	base := apiBaseURL(server)
	job, err := postJSON[map[string]any, jobs.Job](cmd.Context(), client, base+path, map[string]any{})
	if err != nil {
		return fmt.Errorf("%s pool %q: %w", action, poolName, err)
	}
	job, err = waitForJob(cmd.Context(), client, base, job)
	if err != nil {
		return fmt.Errorf("%s pool %q: %w", action, poolName, err)
	}
	switch job.Status {
	case jobs.StatusSucceeded:
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), successMsg, poolName)
		return nil
	default:
		return fmt.Errorf("%s pool %q: %s", action, poolName, poolJobFailureMessage(job))
	}
}

// poolJobFailureMessage renders a job's terminal failure in operator-facing
// terms. quarantine_exhausted (#328) gets its own explicit explanation
// because a bare error code here is exactly the kind of silent-looking
// outcome this issue is about; every other code still surfaces the code
// itself rather than nothing at all.
func poolJobFailureMessage(job jobs.Job) string {
	switch job.ErrorCode {
	case "quarantine_exhausted":
		return "blocked: quarantined resource(s) consume the pool's entire max_total allowance; no capacity was freed. Inspect with `boxy admin pool list`, fix the underlying failure (e.g. a stale guest bootstrap credential), then retry."
	case "pool_config_drained":
		return "pool is configured drained; edit config before filling it"
	case "":
		return fmt.Sprintf("job %s ended %s", job.ID, job.Status)
	default:
		return fmt.Sprintf("job %s failed (error_code=%s)", job.ID, job.ErrorCode)
	}
}

// waitForJob polls GET /api/v1/jobs/{id} until job reaches a terminal
// status, mirroring waitForSandboxReady's polling shape (sandbox_create.go)
// without printing a spinner -- pool maintenance jobs are expected to be
// quick and this is not the primary create/wait UX.
func waitForJob(ctx context.Context, client *http.Client, base string, initial jobs.Job) (jobs.Job, error) {
	job := initial
	ticker := time.NewTicker(sandboxPollInterval)
	defer ticker.Stop()
	for {
		if job.Status.IsTerminal() {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, fmt.Errorf("job %q wait was interrupted: %w", job.ID, ctx.Err())
		case <-ticker.C:
		}
		next, err := fetchJSON[jobs.Job](ctx, client, base+"/api/v1/jobs/"+string(job.ID))
		if err != nil {
			return job, fmt.Errorf("poll job %q: %w", job.ID, err)
		}
		job = next
	}
}
