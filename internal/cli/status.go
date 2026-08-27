package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

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

	cmd.Flags().StringVar(&opts.server, "server", "", "server address (overrides BOXY_SERVER and the global client default)")
	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file to resolve server address")
	return cmd
}

func runStatus(ctx context.Context, opts statusOpts, cmd *cobra.Command) error {
	addr, err := resolveServerAddr(opts, cmd)
	if err != nil {
		return err
	}
	base := apiBaseURL(addr)

	client := apiClientForServer(addr)

	// Health check
	healthy, err := checkHealth(ctx, client, base)
	if err != nil {
		errw := cmd.ErrOrStderr()
		_, _ = fmt.Fprintf(errw, "  Error: cannot reach server at %s\n", addr)
		_, _ = fmt.Fprintf(errw, "  Is `boxy serve` running?\n")
		return MarkReported(err)
	}

	if !healthy {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  Error: server at %s is unhealthy\n", addr)
		return MarkReported(fmt.Errorf("server at %s is unhealthy", addr))
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

	// Sandboxes. Counted deliberately (a switch over the known
	// model.SandboxStatus* constants) rather than by excluding just
	// "failed", so a future status can't silently land in the wrong
	// bucket. A failed sandbox record holds no resources and is never
	// reaped automatically (see #241), so it must not inflate "active".
	sandboxes, err := fetchJSON[[]model.Sandbox](ctx, client, base+"/api/v1/sandboxes")
	if err != nil {
		return fmt.Errorf("fetch sandboxes: %w", err)
	}
	activeSandboxes, failedSandboxes := countSandboxesByStatus(sandboxes)
	_, _ = fmt.Fprintf(out, "  Sandboxes:  %d active, %d failed\n", activeSandboxes, failedSandboxes)

	return nil
}

// countSandboxesByStatus buckets sandboxes into active/failed for the
// status summary. It switches over the known model.SandboxStatus*
// constants rather than excluding just SandboxStatusFailed, so active+
// failed stays exhaustive (every sandbox is counted in exactly one
// bucket) even if a new status is added later without updating this
// switch — an unrecognized status conservatively counts as active,
// preserving today's total-visibility behavior rather than silently
// dropping it from either count.
func countSandboxesByStatus(sandboxes []model.Sandbox) (active, failed int) {
	for _, sb := range sandboxes {
		switch sb.Status {
		case model.SandboxStatusFailed:
			failed++
		default:
			active++
		}
	}
	return active, failed
}

// resolveServerAddr determines the server address with precedence:
// --server flag > BOXY_SERVER > --config server.listen > global client
// default > 127.0.0.1:9090.
func resolveServerAddr(opts statusOpts, cmd *cobra.Command) (string, error) {
	if cmd.Flags().Changed("server") || strings.TrimSpace(opts.server) != "" {
		return opts.server, nil
	}

	if raw := strings.TrimSpace(os.Getenv("BOXY_SERVER")); raw != "" {
		return normalizeClientServer(raw)
	}

	if opts.configPath != "" {
		cfg, err := boxyconfig.LoadFile(opts.configPath)
		if err != nil {
			return "", err
		}
		if cfg.Server.Listen != "" {
			return secureServerURL(displayAddr(cfg.Server.Listen)), nil
		}
	}

	server, err := resolveClientServer(cmd, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(server) == "" {
		return "127.0.0.1:9090", nil
	}
	return server, nil
}

func secureServerURL(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "https://" + addr
}

func checkHealth(ctx context.Context, client *http.Client, base string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, wrapConnError(err, req.URL.Host)
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}
