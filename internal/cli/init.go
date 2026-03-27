package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Boxy configuration in the current directory",
		Long:  "Creates a starter boxy.yaml with a commented example pool definition.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(force)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing boxy.yaml")
	return cmd
}

const configFileName = "boxy.yaml"

func runInit(force bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	dest := filepath.Join(cwd, configFileName)

	if !force {
		if _, err := os.Stat(dest); err == nil {
			return errors.New("boxy.yaml already exists (use --force to overwrite)")
		}
	}

	if err := os.WriteFile(dest, []byte(starterConfig), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configFileName, err)
	}

	fmt.Fprintf(os.Stderr, "  Created %s\n", configFileName)
	fmt.Fprintf(os.Stderr, "\n  Next steps:\n")
	fmt.Fprintf(os.Stderr, "    1. Edit boxy.yaml to define your pools\n")
	fmt.Fprintf(os.Stderr, "    2. boxy config validate     Validate your config\n")
	fmt.Fprintf(os.Stderr, "    3. boxy serve               Start the daemon\n\n")
	return nil
}

const starterConfig = `---
# Boxy configuration
#
# Define pools of pre-warmed resources that can be allocated into sandboxes.
# See docs/cli-wireframe.md for the full CLI reference.

server:
  listen: ":9090"
  # ui: true                    # Web dashboard (default: enabled)

# providers:
#   - type: docker
#     name: docker-local

pools:
  - name: example
    type: docker
    config:
      image: alpine:latest
      command: ["/bin/sh", "-c", "while true; do sleep 3600; done"]
    policy:
      preheat:
        min_ready: 1
        max_total: 3
      recycle:
        max_age: 4h
`
