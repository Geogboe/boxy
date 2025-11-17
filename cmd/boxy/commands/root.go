package commands

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/config"
)

var (
	cfgFile  string
	dbPath   string
	logLevel string
	logger   *logrus.Logger
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "boxy",
	Short: "Boxy - Sandboxing orchestration tool",
	Long: `Boxy is a sandboxing orchestration tool that manages mixed virtual environments
with automatic lifecycle management and pool-based resource provisioning.

It simplifies spinning up VMs, containers, and processes across different platforms
with warm pools for instant allocation.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Initialize logger
		logger = logrus.New()
		level, err := logrus.ParseLevel(logLevel)
		if err != nil {
			level = logrus.InfoLevel
		}
		logger.SetLevel(level)
		logger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
	},
}

// Execute adds all child commands to the root command and sets flags appropriately
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Persistent flags for all commands
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", fmt.Sprintf("config file (default: %s)", config.GetDefaultConfigPath()))
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", fmt.Sprintf("database path (default: %s)", config.GetDefaultDBPath()))
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
}

// loadConfig is a helper to load configuration
func loadConfig() (*config.Config, error) {
	if cfgFile == "" {
		cfgFile = config.GetDefaultConfigPath()
	}

	// Check if config file exists
	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found: %s\nRun 'boxy init' to create a sample configuration", cfgFile)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path if specified
	if dbPath != "" {
		cfg.Storage.Path = dbPath
	}

	return cfg, nil
}
