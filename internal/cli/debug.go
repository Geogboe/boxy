package cli

import "github.com/spf13/cobra"

func newDebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Debugging and testing tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	// `debug provider` (internal/cli/debug_provider*.go) only exercises the
	// devfactory reference driver in-process; it's a development tool, not a
	// release feature, so it's built behind the `devtools` build tag and
	// excluded from release binaries. See #68.
	registerDevtoolsDebugCommands(cmd)
	cmd.AddCommand(newDebugPoolCommand())
	return cmd
}
