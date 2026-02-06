package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Boxy configuration",
	Long:  `Creates a sample configuration file and necessary directories.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check if config already exists
		configPath := config.GetDefaultConfigPath()
		if _, err := os.Stat(configPath); err == nil {
			overwrite, _ := cmd.Flags().GetBool("force")
			if !overwrite {
				return fmt.Errorf("config file already exists: %s\nUse --force to overwrite", configPath)
			}
		}

		// Read example config
		examplePath := "boxy.example.yaml"
		data, err := os.ReadFile(examplePath)
		if err != nil {
			// If example doesn't exist, create a minimal config
			data = []byte(getDefaultConfig())
		}

		// Write config file
		if err := os.WriteFile(configPath, data, 0600); err != nil {
			return fmt.Errorf("failed to write config file: %w", err)
		}

		fmt.Printf("✓ Created config file: %s\n", configPath)
		fmt.Printf("✓ Config directory: %s\n", filepath.Dir(configPath))
		fmt.Printf("\nEdit the config file to define your pools, then run:\n")
		fmt.Printf("  boxy serve    # Start the Boxy service\n")
		fmt.Printf("  boxy pool ls  # List pools and their status\n")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().Bool("force", false, "Overwrite existing config file")
}

func getDefaultConfig() string {
	return `# Boxy Configuration

storage:
  type: sqlite
  path: ./boxy.db

logging:
  level: info
  format: text

pools:
  - name: ubuntu-containers
    type: container
    backend: docker
    image: ubuntu:22.04
    min_ready: 3
    max_total: 10
    cpus: 2
    memory_mb: 512
    health_check_interval: 30s
`
}
