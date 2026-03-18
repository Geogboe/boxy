package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

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
	base := "http://" + addr

	client := &http.Client{Timeout: 5 * time.Second}

	// Health check
	healthy, err := checkHealth(ctx, client, base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error: cannot reach server at %s\n", addr)
		fmt.Fprintf(os.Stderr, "  Is `boxy serve` running?\n")
		return err
	}

	healthStr := "healthy"
	if !healthy {
		healthStr = "unhealthy"
	}
	fmt.Fprintf(os.Stderr, "  Server:     %s (%s)\n", base, healthStr)

	// Pools
	pools, err := fetchJSON[[]model.Pool](ctx, client, base+"/api/v1/pools")
	if err != nil {
		return fmt.Errorf("fetch pools: %w", err)
	}

	totalResources := 0
	for _, p := range pools {
		totalResources += len(p.Inventory.Resources)
	}
	fmt.Fprintf(os.Stderr, "  Pools:      %d configured, %d resources ready\n", len(pools), totalResources)

	// Sandboxes
	sandboxes, err := fetchJSON[[]model.Sandbox](ctx, client, base+"/api/v1/sandboxes")
	if err != nil {
		return fmt.Errorf("fetch sandboxes: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  Sandboxes:  %d active\n", len(sandboxes))

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

func fetchJSON[T any](ctx context.Context, client *http.Client, url string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return zero, fmt.Errorf("decode response from %s: %w", url, err)
	}
	return v, nil
}

