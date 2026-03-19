package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Geogboe/boxy/pkg/store"
	"github.com/spf13/cobra"
)

type sandboxCreateOpts struct {
	file       string
	configPath string
	statePath  string
}

func newSandboxCommand() *cobra.Command {
	var configPath, statePath, file string

	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&configPath, "config", "", "config file path; default: boxy.yaml next to --file or in cwd")
	cmd.PersistentFlags().StringVar(&statePath, "state", "", "state file path; default: .boxy/state.json next to config")
	cmd.PersistentFlags().StringVarP(&file, "file", "f", "", "sandbox spec file (default: sandbox.yaml in cwd)")

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
	cmd.AddCommand(create)

	cmd.AddCommand(newSandboxListCommand(&configPath, &statePath, &file))
	cmd.AddCommand(newSandboxGetCommand(&configPath, &statePath))
	cmd.AddCommand(newSandboxDeleteCommand(&configPath, &statePath))

	return cmd
}

func runSandboxCreate(ctx context.Context, opts sandboxCreateOpts) error {
	if opts.file == "" {
		opts.file = findDefaultSandboxFile()
	}
	if opts.file == "" {
		return fmt.Errorf("no sandbox spec found: pass -f or create sandbox.yaml in cwd")
	}
	return sandboxCreate(ctx, opts)
}

// findDefaultSandboxFile returns "sandbox.yaml" if it exists in the current directory.
func findDefaultSandboxFile() string {
	const defaultName = "sandbox.yaml"
	if _, err := os.Stat(defaultName); err == nil {
		return defaultName
	}
	return ""
}

// resolveSandboxStore opens a DiskStore using the provided config, state, and optional spec paths.
// If statePath is empty, it defaults to .boxy/state.json next to the config file.
func resolveSandboxStore(configPath, statePath, sandboxFile string) (*store.DiskStore, error) {
	if statePath == "" {
		if sandboxFile == "" {
			sandboxFile = findDefaultSandboxFile()
		}
		cfgPath, err := resolveConfigPath(configPath, sandboxFile)
		if err != nil {
			return nil, err
		}
		if cfgPath == "" {
			return nil, fmt.Errorf("no config file found (specify --config, --state, or add sandbox.yaml to cwd)")
		}
		statePath = filepath.Join(filepath.Dir(cfgPath), ".boxy", "state.json")
	}
	return store.NewDiskStore(statePath)
}
