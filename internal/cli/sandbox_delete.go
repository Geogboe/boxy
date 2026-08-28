package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
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
			id, err := validatePathID("sandbox id", args[0])
			if err != nil {
				return err
			}

			client := apiClientForServer(serverAddr())
			base := apiBaseURL(serverAddr())

			sb, err := deleteJSON[model.Sandbox](cmd.Context(), client, base+"/api/v1/sandboxes/"+id)
			if err != nil {
				var apiErr *apiError
				if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
					return fmt.Errorf("sandbox %q not found", args[0])
				}
				return fmt.Errorf("delete sandbox %q: %w", args[0], err)
			}
			for _, cleanupErr := range deleteGuestCredentials(serverAddr(), sb.ID, sb.Resources) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  Warning: %v\n", cleanupErr)
			}
			if noWait {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "accepted deletion of sandbox %s\n", sb.ID)
				return nil
			}
			if err := waitForSandboxDeleted(cmd.Context(), cmd.OutOrStdout(), client, base, sb); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted sandbox %s\n", sb.ID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Return after delete request is accepted")
	return cmd
}

// waitForSandboxDeleted polls until the sandbox is fully torn down (the
// server purges the record once every resource is destroyed — see
// internal/sandbox.DeletionReconciler.cleanupSandbox). While it waits, it
// reports progress by watching sb.Resources shrink: cleanupSandbox destroys
// resources one at a time and persists the sandbox after each removal, so
// each poll's resource count is a real, live destroy-progress signal, not
// just an opaque "still deleting" state.
func waitForSandboxDeleted(ctx context.Context, out io.Writer, client *http.Client, base string, sb model.Sandbox) error {
	id := sb.ID
	total := len(sb.Resources)
	remaining := total

	ticker := time.NewTicker(sandboxPollInterval)
	defer ticker.Stop()

	for {
		next, err := fetchJSON[model.Sandbox](ctx, client, base+"/api/v1/sandboxes/"+string(id))
		if err == nil {
			if total > 0 {
				if n := len(next.Resources); n != remaining {
					remaining = n
					_, _ = fmt.Fprintf(out, "  %d/%d resource(s) destroyed\n", total-remaining, total)
				}
			}
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
				if total > 0 && remaining != 0 {
					_, _ = fmt.Fprintf(out, "  %d/%d resource(s) destroyed\n", total, total)
				}
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
