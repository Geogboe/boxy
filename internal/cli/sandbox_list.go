package cli

import (
	"github.com/spf13/cobra"
)

func newSandboxListCommand(configPath, statePath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := resolveSandboxStore(*configPath, *statePath)
			if err != nil {
				return err
			}
			sbs, err := st.ListSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(sbs)
		},
	}
}
