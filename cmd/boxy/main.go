package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Geogboe/boxy/internal/cli"
)

func main() {
	// Minimal fallback logger until PersistentPreRunE configures slog from flags.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand()
	if err := root.ExecuteContext(ctx); err != nil {
		slog.Error("command failed", "err", err)
		os.Exit(1)
	}
}
