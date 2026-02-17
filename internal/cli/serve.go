package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	boxyconfig "github.com/Geogboe/boxy/v2/internal/config"
	"github.com/Geogboe/boxy/v2/internal/core/pool"
	"github.com/Geogboe/boxy/v2/internal/core/sandbox"
	"github.com/Geogboe/boxy/v2/internal/core/store"
	"github.com/Geogboe/boxy/v2/pkg/providersdk"
	"github.com/Geogboe/boxy/v2/pkg/providersdk/builtins"
	"github.com/spf13/cobra"
)

type serveOpts struct {
	configPath string
	once       bool
}

func newServeCommand() *cobra.Command {
	var opts serveOpts

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Boxy core (placeholder)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present")
	cmd.Flags().BoolVar(&opts.once, "once", false, "initialize and validate then exit")

	return cmd
}

func runServe(ctx context.Context, opts serveOpts) error {
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

	if opts.once {
		slog.Info("serve init complete (--once); exiting")
		return nil
	}

	slog.Info("serve started")
	return serveLoop(ctx)
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

	candidates := []string{
		filepath.Join(cwd, "boxy.yaml"),
		filepath.Join(cwd, "boxy.yml"),
	}

	for _, p := range candidates {
		_, err := os.Stat(p)
		if err == nil {
			return p, nil
		}
		if os.IsNotExist(err) {
			continue
		}
		return "", fmt.Errorf("stat %q: %w", p, err)
	}
	return "", nil
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
			slog.Info("reconcile tick (stub)")
		}
	}
}

func providerTypes(reg *providersdk.Registry) []string {
	ts := reg.Types()
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, string(t))
	}
	return out
}
