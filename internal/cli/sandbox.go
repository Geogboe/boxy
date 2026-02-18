package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

type sandboxCreateOpts struct {
	file       string
	configPath string
	statePath  string
}

func newSandboxCommand() *cobra.Command {
	var createOpts sandboxCreateOpts

	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a sandbox from a spec file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSandboxCreate(cmd.Context(), createOpts)
		},
	}
	create.Flags().StringVarP(&createOpts.file, "file", "f", "", "sandbox spec file path (.yaml/.yml)")
	create.Flags().StringVar(&createOpts.configPath, "config", "", "config file path (.yaml/.yml/.json); default: boxy.yaml next to --file, else cwd")
	create.Flags().StringVar(&createOpts.statePath, "state", "", "state file path; default: .boxy/state.json next to config")
	_ = create.MarkFlagRequired("file")

	cmd.AddCommand(create)
	return cmd
}

func runSandboxCreate(ctx context.Context, opts sandboxCreateOpts) error {
	if opts.file == "" {
		return fmt.Errorf("--file is required")
	}
	return sandboxCreate(ctx, opts)
}
