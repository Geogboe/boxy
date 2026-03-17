package cli

import (
	"errors"
	"fmt"

	"github.com/Geogboe/boxy/v2/pkg/model"
	"github.com/Geogboe/boxy/v2/pkg/store"
	"github.com/spf13/cobra"
)

func newSandboxDeleteCommand(configPath, statePath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a sandbox by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := resolveSandboxStore(*configPath, *statePath)
			if err != nil {
				return err
			}
			id := model.SandboxID(args[0])
			if err := st.DeleteSandbox(cmd.Context(), id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("sandbox %q not found", args[0])
				}
				return err
			}
			fmt.Printf("deleted sandbox %s\n", id)
			return nil
		},
	}
}
