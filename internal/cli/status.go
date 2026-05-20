package cli

import (
	"context"
	"fmt"
	"net/http"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/spf13/cobra"
)

type statusOpts struct {
	server     string
	configPath string
}

func newStatusCommand() *cobra.Command {
	var opts statusOpts

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check server health and summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), opts, cmd)
		},
	}

	cmd.Flags().StringVar(&opts.server, "server", "", "server address (default 127.0.0.1:9090)")
	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file to resolve server address")
	return cmd
}

func runStatus(ctx context.Context, opts statusOpts, cmd *cobra.Command) error {
	addr := resolveServerAddr(opts, cmd)
	base := apiBaseURL(addr)

	client := defaultAPIClient()

	// Health check
	healthy, err := checkHealth(ctx, client, base)
	if err != nil {
		errw := cmd.ErrOrStderr()
		_, _ = fmt.Fprintf(errw, "  Error: cannot reach server at %s\n", addr)
		_, _ = fmt.Fprintf(errw, "  Is `boxy serve` running?\n")
		return err
	}

	if !healthy {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  Error: server at %s is unhealthy\n", addr)
		return fmt.Errorf("server at %s is unhealthy", addr)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "  Server:     %s (healthy)\n", base)

	// Pools
	pools, err := fetchJSON[[]model.Pool](ctx, client, base+"/api/v1/pools")
	if err != nil {
		return fmt.Errorf("fetch pools: %w", err)
	}

	totalResources := 0
	for _, p := range pools {
		totalResources += len(p.Inventory.Resources)
	}
	_, _ = fmt.Fprintf(out, "  Pools:      %d configured, %d resources ready\n", len(pools), totalResources)

	// Sandboxes
	sandboxes, err := fetchJSON[[]model.Sandbox](ctx, client, base+"/api/v1/sandboxes")
	if err != nil {
		return fmt.Errorf("fetch sandboxes: %w", err)
	}
	_, _ = fmt.Fprintf(out, "  Sandboxes:  %d active\n", len(sandboxes))

	return nil
}

// resolveServerAddr determines the server address with precedence:
// --server flag > config server.listen > default 127.0.0.1:9090
func resolveServerAddr(opts statusOpts, cmd *cobra.Command) string {
	if cmd.Flags().Changed("server") {
		return opts.server
	}

	if opts.configPath != "" {
		cfg, err := boxyconfig.LoadFile(opts.configPath)
		if err == nil && cfg.Server.Listen != "" {
			return displayAddr(cfg.Server.Listen)
		}
	}

	return "127.0.0.1:9090"
}

func checkHealth(ctx context.Context, client *http.Client, base string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}
