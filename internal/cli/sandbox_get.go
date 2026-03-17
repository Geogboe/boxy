package cli

import (
	"errors"
	"fmt"

	"github.com/Geogboe/boxy/v2/pkg/model"
	"github.com/Geogboe/boxy/v2/pkg/store"
	"github.com/spf13/cobra"
)

func newSandboxGetCommand(configPath, statePath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a sandbox by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := resolveSandboxStore(*configPath, *statePath)
			if err != nil {
				return err
			}
			sb, err := st.GetSandbox(cmd.Context(), model.SandboxID(args[0]))
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("sandbox %q not found", args[0])
				}
				return err
			}
			return printJSON(sb)
		},
	}
}
