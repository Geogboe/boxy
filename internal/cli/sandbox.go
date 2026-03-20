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
	opts.file = resolveRelative(opts.file)
	opts.configPath = resolveRelative(opts.configPath)
	return sandboxCreate(ctx, opts)
}

// resolveRelative resolves a relative path against the effective working directory.
// Absolute paths and empty strings are returned unchanged.
func resolveRelative(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	wd, err := effectiveWD()
	if err != nil {
		return p
	}
	return filepath.Join(wd, p)
}

// effectiveWD returns the working directory to use for config/state lookup.
// It checks BOXY_WORKING_DIR first (set by the Taskfile go:run task so that
// `task run` preserves the caller's directory even when go runs from ROOT_DIR),
// then falls back to os.Getwd().
func effectiveWD() (string, error) {
	if d := os.Getenv("BOXY_WORKING_DIR"); d != "" {
		return d, nil
	}
	return os.Getwd()
}

// findDefaultSandboxFile returns "sandbox.yaml" if it exists in the effective working directory.
func findDefaultSandboxFile() string {
	wd, err := effectiveWD()
	if err != nil {
		return ""
	}
	p := filepath.Join(wd, "sandbox.yaml")
	if _, err := os.Stat(p); err == nil {
		return p
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
