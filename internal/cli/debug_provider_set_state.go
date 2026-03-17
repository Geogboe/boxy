package cli

import (
	"github.com/Geogboe/boxy/pkg/providersdk/providers/devfactory"
	"github.com/spf13/cobra"
)

func newDebugProviderSetStateCommand(opts *debugProviderOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "set-state <id> <state>",
		Short: "Change resource state",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			d := newDebugProviderDriver(opts)
			result, err := d.Update(cmd.Context(), args[0], &devfactory.SetStateOp{State: args[1]})
			if err != nil {
				return err
			}
			return printJSON(result)
		},
	}
}
