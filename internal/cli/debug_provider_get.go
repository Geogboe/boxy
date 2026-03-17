package cli

import (
	"github.com/spf13/cobra"
)

func newDebugProviderGetCommand(opts *debugProviderOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Read resource state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d := newDebugProviderDriver(opts)
			status, err := d.Read(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(status)
		},
	}
}
