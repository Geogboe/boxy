package cli

import (
	"context"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func newAgentCommand() *cobra.Command {
	var server string

	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage remote agents and registration tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&server, "server", "", "server address (default 127.0.0.1:9090)")

	serverAddr := func() string { return server }

	cmd.AddCommand(newAgentTokenCommand(serverAddr))
	cmd.AddCommand(newAgentListCommand(serverAddr))
	cmd.AddCommand(newAgentRevokeCommand(serverAddr))

	return cmd
}

// agentSummary mirrors internal/pool.AgentSummary's JSON shape. Redeclared
// here because the CLI talks to the daemon over REST, not by importing its
// internals — same convention as the sandbox commands.
type agentSummary struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Providers []providersdk.Type `json:"providers"`
	Available bool               `json:"available"`
}

func newAgentListCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered agents and their availability",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())
			agents, err := fetchJSON[[]agentSummary](cmd.Context(), client, base+"/api/v1/agents")
			if err != nil {
				return err
			}
			if len(agents) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no agents registered")
				return nil
			}
			for _, a := range agents {
				status := "available"
				if !a.Available {
					status = "unavailable"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%v\t%s\n", a.ID, a.Name, a.Providers, status)
			}
			return nil
		},
	}
}

func newAgentRevokeCommand(serverAddr func() string) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an agent's identity and tear down its connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())
			body := struct {
				Reason string `json:"reason,omitempty"`
			}{Reason: reason}
			if err := deleteNoContentWithBody(cmd.Context(), client, base+"/api/v1/agents/"+id, body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "revoked agent %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "optional reason recorded with the revocation")
	return cmd
}

// deleteNoContent issues a DELETE and expects a 2xx response with no body
// (the agent/token endpoints return 204, unlike the sandbox endpoints'
// 202-with-body shape that deleteJSON decodes).
func deleteNoContent(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	return doNoContent(client, req)
}

func deleteNoContentWithBody[T any](ctx context.Context, client *http.Client, url string, body T) error {
	buf, err := encodeJSONBody(body)
	if err != nil {
		return fmt.Errorf("encode request for %s: %w", url, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doNoContent(client, req)
}

func doNoContent(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req) //nolint:gosec // CLI requests intentionally target the user-configured Boxy server.
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(resp, req.URL.String())
	}
	return nil
}
