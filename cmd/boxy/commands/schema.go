package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/internal/config"
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Print or write the JSON Schema for boxy.yaml",
	Long: `Outputs the JSON Schema that describes valid boxy.yaml configuration files.

By default the schema is printed to stdout. Use --output to write it as a file
to a given directory, producing <dir>/.boxy-schema.json.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		outputDir, _ := cmd.Flags().GetString("output")

		if outputDir == "" {
			// Print to stdout
			fmt.Print(string(config.SchemaJSON))
			return nil
		}

		// Write to directory
		dest := filepath.Join(outputDir, config.SchemaFileName)
		if err := os.WriteFile(dest, config.SchemaJSON, 0600); err != nil {
			return fmt.Errorf("failed to write schema file: %w", err)
		}

		fmt.Printf("✓ Wrote schema to %s\n", dest)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(schemaCmd)
	schemaCmd.Flags().StringP("output", "o", "", "directory to write "+config.SchemaFileName+" into")
}
