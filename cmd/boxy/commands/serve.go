package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/server"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Boxy service",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		logger.Info("Starting Boxy service")

		rt, err := server.Start(ctx, cfg, logger)
		if err != nil {
			return err
		}
		defer rt.Stop(logger)

		logger.Info("✓ Boxy service started successfully")
		fmt.Printf("\n✓ Boxy service is running\n")
		fmt.Printf("  • %d pools active\n", len(rt.Pools))
		fmt.Printf("  • Database: %s\n", cfg.Storage.Path)
		fmt.Printf("\nPress Ctrl+C to stop\n\n")

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigChan:
		case <-ctx.Done():
		}

		logger.Info("Shutting down Boxy service...")
		fmt.Printf("\nShutting down gracefully...\n")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
