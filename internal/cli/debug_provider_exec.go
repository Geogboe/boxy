package cli

import (
	"fmt"

	"github.com/Geogboe/boxy/v2/pkg/providersdk/providers/devfactory"
	"github.com/spf13/cobra"
)

func newDebugProviderExecCommand(opts *debugProviderOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <id> -- <cmd> [args...]",
		Short: "Simulate command execution on a resource",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			cmdArgs := args[1:]

			// Strip leading "--" if present.
			if len(cmdArgs) > 0 && cmdArgs[0] == "--" {
				cmdArgs = cmdArgs[1:]
			}
			if len(cmdArgs) == 0 {
				return fmt.Errorf("usage: boxy debug provider exec <id> -- <command> [args...]")
			}

			d := newDebugProviderDriver(opts)
			result, err := d.Update(cmd.Context(), id, &devfactory.ExecOp{Command: cmdArgs})
			if err != nil {
				return err
			}
			return printJSON(result)
		},
	}

	cmd.Flags().SetInterspersed(false)
	return cmd
}
