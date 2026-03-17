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

	cmd.AddCommand(newDebugProviderCommand())
	return cmd
}
