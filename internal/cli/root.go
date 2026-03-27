package cli

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type rootOpts struct {
	logLevel string
	logFile  string
}

func NewRootCommand() *cobra.Command {
	var opts rootOpts

	root := &cobra.Command{
		Use:           "boxy",
		Short:         "Boxy is a resource pooling and sandbox orchestration tool",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return setupLogging(opts)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetVersionTemplate("boxy {{.Version}}\n")

	root.PersistentFlags().StringVar(&opts.logLevel, "log-level", "info", "log verbosity (debug, info, warn, error)")
	root.PersistentFlags().StringVar(&opts.logFile, "log-file", "", "write structured logs to file instead of stderr")

	root.AddCommand(newServeCommand())
	root.AddCommand(newConfigCommand())
	root.AddCommand(newSandboxCommand())
	root.AddCommand(newDebugCommand())
	root.AddCommand(newInitCommand())
	root.AddCommand(newStatusCommand())
	root.AddCommand(newVersionCommand())
	return root
}

func setupLogging(opts rootOpts) error {
	level, err := parseLogLevel(opts.logLevel)
	if err != nil {
		return err
	}

	var w *os.File
	if opts.logFile != "" {
		f, err := os.OpenFile(opts.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open log file %q: %w", opts.logFile, err)
		}
		w = f
	} else {
		w = os.Stderr
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	})))
	return nil
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q (valid: debug, info, warn, error)", s)
	}
}
