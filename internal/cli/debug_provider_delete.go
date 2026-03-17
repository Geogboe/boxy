package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDebugProviderDeleteCommand(opts *debugProviderOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d := newDebugProviderDriver(opts)
			if err := d.Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("deleted %s\n", args[0])
			return nil
		},
	}
}
