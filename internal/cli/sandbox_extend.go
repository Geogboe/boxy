package cli

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/spf13/cobra"
)

type extendSandboxRequest struct {
	Duration string `json:"duration"`
}

func newSandboxExtendCommand(serverAddr func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extend <id> <duration>",
		Short: "Push a sandbox's auto-destroy expiry further out",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, durationArg := args[0], args[1]
			if _, err := time.ParseDuration(durationArg); err != nil {
				return fmt.Errorf("invalid duration %q: %w", durationArg, err)
			}

			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())

			sb, err := postJSON[extendSandboxRequest, model.Sandbox](
				cmd.Context(),
				client,
				base+"/api/v1/sandboxes/"+id+"/extend",
				extendSandboxRequest{Duration: durationArg},
			)
			if err != nil {
				var apiErr *apiError
				if errors.As(err, &apiErr) {
					switch apiErr.StatusCode {
					case http.StatusNotFound:
						return fmt.Errorf("sandbox %q not found", id)
					case http.StatusConflict:
						return fmt.Errorf("extend sandbox %q: %s", id, apiErr.Message)
					}
				}
				return fmt.Errorf("extend sandbox %q: %w", id, err)
			}

			expiry := "no expiry"
			if sb.ExpiresAt != nil {
				expiry = sb.ExpiresAt.Local().Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "extended sandbox %s, expires at %s\n", sb.ID, expiry)
			return nil
		},
	}
	return cmd
}
