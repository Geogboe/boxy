package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/pool"
	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/builtins"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

const defaultListenAddr = ":9090"

type serveOpts struct {
	configPath string
	listen     string
	ui         bool
}

func newServeCommand() *cobra.Command {
	var opts serveOpts

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Boxy daemon (API server + reconcile loop)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), opts, cmd)
		},
	}

	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	cmd.Flags().StringVar(&opts.listen, "listen", "", "HTTP listen address (default :9090)")
	cmd.Flags().BoolVar(&opts.ui, "ui", true, "enable web dashboard UI")

	return cmd
}

func runServe(ctx context.Context, opts serveOpts, cmd *cobra.Command) error {
	cfg, cfgPath, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	if cfgPath == "" {
		slog.Warn("no config file found; starting with empty config (providers=0)")
	} else {
		slog.Info("loaded config", "path", cfgPath, "providers", len(cfg.Providers))
	}

	reg := providersdk.NewRegistry()
	if err := builtins.RegisterBuiltins(reg); err != nil {
		return fmt.Errorf("register builtin providers: %w", err)
	}
	slog.Info("builtin provider types", "types", providerTypes(reg))

	if err := reg.ValidateInstances(ctx, cfg.Providers); err != nil {
		return fmt.Errorf("validate providers: %w", err)
	}
	slog.Info("providers validated", "count", len(cfg.Providers))

	st := store.NewMemoryStore()
	_ = sandbox.New(st)
	_ = pool.New(st, pool.UnimplementedProvisioner{})
	slog.Info("core initialized", "store", "memory")

	// Resolve listen address: flag > config > default
	listenAddr := resolveListenAddr(opts, cmd, cfg)

	// Resolve UI enabled: flag > config > default (true)
	uiEnabled := resolveUIEnabled(opts, cmd, cfg)

	srv := server.New(st, listenAddr, uiEnabled)
	slog.Info("starting server", "listen", listenAddr, "ui", uiEnabled)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return srv.Start(ctx)
	})
	g.Go(func() error {
		return serveLoop(ctx)
	})

	printServeBanner(listenAddr, uiEnabled, len(cfg.Pools))

	return g.Wait()
}

// resolveListenAddr picks the listen address with precedence:
// explicit --listen flag > config server.listen > default :9090
func resolveListenAddr(opts serveOpts, cmd *cobra.Command, cfg boxyconfig.Config) string {
	if cmd.Flags().Changed("listen") {
		return opts.listen
	}
	if cfg.Server.Listen != "" {
		return cfg.Server.Listen
	}
	return defaultListenAddr
}

// resolveUIEnabled picks the UI toggle with precedence:
// explicit --ui flag > config server.ui > default true
func resolveUIEnabled(opts serveOpts, cmd *cobra.Command, cfg boxyconfig.Config) bool {
	if cmd.Flags().Changed("ui") {
		return opts.ui
	}
	return cfg.Server.UIEnabled()
}

func loadConfig(explicitPath string) (cfg boxyconfig.Config, usedPath string, _ error) {
	if explicitPath != "" {
		c, err := boxyconfig.LoadFile(explicitPath)
		if err != nil {
			return boxyconfig.Config{}, "", err
		}
		return c, explicitPath, nil
	}

	path, err := findDefaultConfigPath()
	if err != nil {
		return boxyconfig.Config{}, "", err
	}
	if path == "" {
		return boxyconfig.Config{}, "", nil
	}

	c, err := boxyconfig.LoadFile(path)
	if err != nil {
		return boxyconfig.Config{}, "", err
	}
	return c, path, nil
}

func findDefaultConfigPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	return findConfigPathInDir(cwd)
}

func serveLoop(ctx context.Context) error {
	const tickEvery = 10 * time.Second

	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			slog.Debug("reconcile tick")
		}
	}
}

// printServeBanner writes a human-friendly startup message to stderr.
func printServeBanner(listenAddr string, uiEnabled bool, poolCount int) {
	host := displayAddr(listenAddr)

	fmt.Fprintf(os.Stderr, "\n  Boxy server running\n\n")
	if uiEnabled {
		fmt.Fprintf(os.Stderr, "    Dashboard:  http://%s/\n", host)
	}
	fmt.Fprintf(os.Stderr, "    API:        http://%s/api/v1/\n", host)
	fmt.Fprintf(os.Stderr, "    Health:     http://%s/healthz\n", host)
	fmt.Fprintf(os.Stderr, "\n  Pools: %d configured\n", poolCount)
	fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop\n\n")
}

// displayAddr resolves a listen address for display.
// ":9090" becomes "127.0.0.1:9090"; "0.0.0.0:9090" becomes "127.0.0.1:9090".
func displayAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "127.0.0.1" + addr
	}
	if len(addr) > 7 && addr[:7] == "0.0.0.0" {
		return "127.0.0.1" + addr[7:]
	}
	return addr
}

func providerTypes(reg *providersdk.Registry) []string {
	ts := reg.Types()
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, string(t))
	}
	return out
}
