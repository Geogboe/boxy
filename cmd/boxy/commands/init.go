package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/config"
)

// schemaComment is the YAML language server directive that enables editor
// autocompletion and validation when the YAML extension is installed.
const schemaComment = "# yaml-language-server: $schema=./" + config.SchemaFileName

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

		// Prepend schema comment if not already present
		content := string(data)
		if !strings.Contains(content, "yaml-language-server") {
			content = schemaComment + "\n" + content
		}

		// Write config file
		if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to write config file: %w", err)
		}

		// Write JSON Schema file alongside config
		schemaPath := filepath.Join(filepath.Dir(configPath), config.SchemaFileName)
		if err := os.WriteFile(schemaPath, config.SchemaJSON, 0600); err != nil {
			return fmt.Errorf("failed to write schema file: %w", err)
		}

		fmt.Printf("✓ Created config file: %s\n", configPath)
		fmt.Printf("✓ Created schema file: %s\n", schemaPath)
		fmt.Printf("✓ Config directory: %s\n", filepath.Dir(configPath))
		fmt.Printf("\nEdit the config file to define your pools, then run:\n")
		fmt.Printf("  boxy serve    # Start the Boxy service\n")
		fmt.Printf("  boxy pool ls  # List pools and their status\n")
		fmt.Printf("\nTip: Install the YAML extension in VS Code for config autocompletion.\n")

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
