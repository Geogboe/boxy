package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Geogboe/boxy/v2/pkg/store"
	"github.com/spf13/cobra"
)

type sandboxCreateOpts struct {
	file       string
	configPath string
	statePath  string
}

func newSandboxCommand() *cobra.Command {
	var configPath, statePath string

	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&configPath, "config", "", "config file path (.yaml/.yml/.json); default: boxy.yaml next to --file, else cwd")
	cmd.PersistentFlags().StringVar(&statePath, "state", "", "state file path; default: .boxy/state.json next to config")

	// create subcommand
	var file string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a sandbox from a spec file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSandboxCreate(cmd.Context(), sandboxCreateOpts{
				file:       file,
				configPath: configPath,
				statePath:  statePath,
			})
		},
	}
	create.Flags().StringVarP(&file, "file", "f", "", "sandbox spec file path (.yaml/.yml)")
	_ = create.MarkFlagRequired("file")
	cmd.AddCommand(create)

	cmd.AddCommand(newSandboxListCommand(&configPath, &statePath))
	cmd.AddCommand(newSandboxGetCommand(&configPath, &statePath))
	cmd.AddCommand(newSandboxDeleteCommand(&configPath, &statePath))

	return cmd
}

func runSandboxCreate(ctx context.Context, opts sandboxCreateOpts) error {
	if opts.file == "" {
		return fmt.Errorf("--file is required")
	}
	return sandboxCreate(ctx, opts)
}

// resolveSandboxStore opens a DiskStore using the provided config and state paths.
// If statePath is empty, it defaults to .boxy/state.json next to the config file.
func resolveSandboxStore(configPath, statePath string) (*store.DiskStore, error) {
	if statePath == "" {
		cfgPath, err := resolveConfigPath(configPath, "")
		if err != nil {
			return nil, err
		}
		if cfgPath == "" {
			return nil, fmt.Errorf("no config file found (specify --config or --state)")
		}
		statePath = filepath.Join(filepath.Dir(cfgPath), ".boxy", "state.json")
	}
	return store.NewDiskStore(statePath)
}
