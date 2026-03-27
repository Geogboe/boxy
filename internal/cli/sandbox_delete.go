package cli

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

func newSandboxDeleteCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a sandbox by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())

			status, err := deleteAPI(cmd.Context(), client, base+"/api/v1/sandboxes/"+args[0])
			if err != nil {
				return fmt.Errorf("delete sandbox %q: %w", args[0], err)
			}

			switch status {
			case http.StatusNoContent:
				fmt.Printf("deleted sandbox %s\n", args[0])
				return nil
			case http.StatusNotFound:
				return fmt.Errorf("sandbox %q not found", args[0])
			default:
				return fmt.Errorf("delete sandbox %q: unexpected status %d", args[0], status)
			}
		},
	}
}
