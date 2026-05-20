package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/spf13/cobra"
)

func newSandboxDeleteCommand(serverAddr func() string) *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a sandbox by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())
			id := args[0]

			sb, err := deleteJSON[model.Sandbox](cmd.Context(), client, base+"/api/v1/sandboxes/"+id)
			if err != nil {
				var apiErr *apiError
				if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
					return fmt.Errorf("sandbox %q not found", id)
				}
				return fmt.Errorf("delete sandbox %q: %w", id, err)
			}
			if noWait {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "accepted deletion of sandbox %s\n", sb.ID)
				return nil
			}
			if err := waitForSandboxDeleted(cmd.Context(), client, base, sb.ID); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted sandbox %s\n", sb.ID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Return after delete request is accepted")
	return cmd
}

func waitForSandboxDeleted(ctx context.Context, client *http.Client, base string, id model.SandboxID) error {
	ticker := time.NewTicker(sandboxPollInterval)
	defer ticker.Stop()

	for {
		_, err := fetchJSON[model.Sandbox](ctx, client, base+"/api/v1/sandboxes/"+string(id))
		if err == nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("sandbox %q deletion accepted but wait was interrupted: %w", id, ctx.Err())
			case <-ticker.C:
			}
			continue
		}
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == http.StatusNotFound {
				return nil
			}
			return fmt.Errorf("wait for sandbox %q deletion: %w", id, err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("sandbox %q deletion accepted but wait was interrupted: %w", id, err)
		}
		return fmt.Errorf("wait for sandbox %q deletion: %w", id, err)
	}
}
