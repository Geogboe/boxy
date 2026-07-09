package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newAgentTokenCommand(serverAddr func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage single-use agent registration tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAgentTokenCreateCommand(serverAddr))
	cmd.AddCommand(newAgentTokenListCommand(serverAddr))
	cmd.AddCommand(newAgentTokenRevokeCommand(serverAddr))
	return cmd
}

type createAgentTokenRequest struct {
	Label string `json:"label,omitempty"`
	TTL   string `json:"ttl,omitempty"`
}

type createAgentTokenResponse struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	Label     string    `json:"label,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

func newAgentTokenCreateCommand(serverAddr func() string) *cobra.Command {
	var label, ttl string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a single-use registration token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if ttl != "" {
				if _, err := time.ParseDuration(ttl); err != nil {
					return fmt.Errorf("invalid ttl %q: %w", ttl, err)
				}
			}
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())
			resp, err := postJSON[createAgentTokenRequest, createAgentTokenResponse](
				cmd.Context(), client, base+"/api/v1/agent-tokens",
				createAgentTokenRequest{Label: label, TTL: ttl},
			)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "token: %s\n", resp.Token)
			_, _ = fmt.Fprintf(out, "id: %s\n", resp.ID)
			if resp.Label != "" {
				_, _ = fmt.Fprintf(out, "label: %s\n", resp.Label)
			}
			_, _ = fmt.Fprintf(out, "expires: %s\n", resp.ExpiresAt.Format(time.RFC3339))
			_, _ = fmt.Fprintln(out, "The token is shown once and never stored — pass it to `boxy agent serve --token <token>` before it expires.")
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "optional operator note (e.g. the host this token is for)")
	cmd.Flags().StringVar(&ttl, "ttl", "", "token validity as a Go duration (default 1h)")
	return cmd
}

type agentTokenSummary struct {
	ID        string    `json:"id"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
}

func newAgentTokenListCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registration tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())
			toks, err := fetchJSON[[]agentTokenSummary](cmd.Context(), client, base+"/api/v1/agent-tokens")
			if err != nil {
				return err
			}
			if len(toks) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no registration tokens")
				return nil
			}
			for _, tok := range toks {
				state := "unused"
				if tok.Used {
					state = "used"
				} else if time.Now().After(tok.ExpiresAt) {
					state = "expired"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\texpires %s\n", tok.ID, tok.Label, state, tok.ExpiresAt.Format(time.RFC3339))
			}
			return nil
		},
	}
}

func newAgentTokenRevokeCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an unredeemed registration token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())
			if err := deleteNoContent(cmd.Context(), client, base+"/api/v1/agent-tokens/"+id); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "revoked token %s\n", id)
			return nil
		},
	}
}

// encodeJSONBody marshals v into a buffer for request bodies that aren't
// covered by the postJSON helper (e.g. DELETE with a body).
func encodeJSONBody[T any](v T) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, err
	}
	return buf, nil
}
