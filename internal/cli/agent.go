package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	"github.com/Geogboe/boxy/pkg/humanize"
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
	cmd.PersistentFlags().StringVar(&server, "server", "", "server address (overrides BOXY_SERVER and the global client default)")

	serverAddr := func() string { return server }

	cmd.AddCommand(newAgentTokenCommand(serverAddr))
	cmd.AddCommand(newAgentListCommand(serverAddr))
	cmd.AddCommand(newAgentStatusCommand(serverAddr))
	cmd.AddCommand(newAgentRevokeCommand(serverAddr))
	cmd.AddCommand(newAgentServeCommand())
	cmd.AddCommand(newAgentServiceCommand())

	return cmd
}

// agentSummary mirrors internal/pool.AgentSummary's JSON shape. Redeclared
// here because the CLI talks to the daemon over REST, not by importing its
// internals — same convention as the sandbox commands. Availability's value
// type is the real providersdk.ResourceAvailability (not a further
// redeclaration): providersdk is already a public pkg/ dependency this file
// imports, not internal application code, so there's no coupling reason to
// shadow it, and reusing it directly means a future field addition there
// shows up here for free instead of silently staying MemoryMB-only.
type agentSummary struct {
	ID             string                                                `json:"id"`
	Name           string                                                `json:"name"`
	Providers      []providersdk.Type                                    `json:"providers"`
	Available      bool                                                  `json:"available"`
	Connected      bool                                                  `json:"connected"`
	LastSeen       *time.Time                                            `json:"last_seen,omitempty"`
	Availability   map[providersdk.Type]providersdk.ResourceAvailability `json:"availability,omitempty"`
	AvailabilityAt *time.Time                                            `json:"availability_at,omitempty"`
}

func newAgentListCommand(serverAddr func() string) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered agents, connection liveness, and availability",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateListFormat(format); err != nil {
				return err
			}

			client := apiClientForServer(serverAddr())
			base := apiBaseURL(serverAddr())
			agents, err := fetchJSON[[]agentSummary](cmd.Context(), client, base+"/api/v1/agents")
			if err != nil {
				return err
			}
			if format == "json" {
				return printJSON(agents)
			}
			if len(agents) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no agents registered")
				return nil
			}
			rows := [][]string{{"ID", "NAME", "PROVIDERS", "CONNECTION", "SCHEDULING", "LAST HEARTBEAT"}}
			for _, a := range agents {
				rows = append(rows, []string{
					a.ID,
					a.Name,
					fmt.Sprint(a.Providers),
					connectionLabel(a.Connected),
					schedulingLabel(a.Available),
					lastSeenLabel(a.LastSeen),
				})
			}
			return pterm.DefaultTable.WithHasHeader().WithData(rows).Render()
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "output format: json or table (default table)")
	return cmd
}

func newAgentStatusCommand(serverAddr func() string) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "status <id>",
		Short: "Show one agent's connection liveness, scheduling eligibility, and reported capacity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateListFormat(format); err != nil {
				return err
			}
			// Trim and reject empty like validatePathID, but don't URL-escape:
			// id is compared against the raw AgentSummary.ID below, not built
			// into a request path.
			id := strings.TrimSpace(args[0])
			if id == "" {
				return fmt.Errorf("agent id must not be empty")
			}

			// GET /api/v1/agents has no by-ID variant; the daemon only exposes
			// the full snapshot, so filter client-side rather than adding new
			// server surface for a single CLI convenience command.
			client := apiClientForServer(serverAddr())
			base := apiBaseURL(serverAddr())
			agents, err := fetchJSON[[]agentSummary](cmd.Context(), client, base+"/api/v1/agents")
			if err != nil {
				return err
			}
			var found *agentSummary
			for i := range agents {
				if agents[i].ID == id {
					found = &agents[i]
					break
				}
			}
			if found == nil {
				return fmt.Errorf("agent %q not found", args[0])
			}

			if format == "json" {
				return printJSON(found)
			}
			rows := [][]string{
				{"ID", found.ID},
				{"Name", found.Name},
				{"Providers", fmt.Sprint(found.Providers)},
				{"Connection", connectionLabel(found.Connected)},
				{"Scheduling", schedulingLabel(found.Available)},
				{"Last heartbeat", lastSeenLabel(found.LastSeen)},
			}
			// Iterate the ordered Providers slice (not the Availability map
			// directly) for a deterministic row order, and so a provider with
			// no sample yet still gets its own "no capacity sample" row
			// instead of silently vanishing — same pattern as ui.go's
			// agentsData.
			if len(found.Providers) == 0 {
				rows = append(rows, []string{"Capacity", "no providers registered"})
			}
			for _, provider := range found.Providers {
				label := "Capacity (" + string(provider) + ")"
				if avail, ok := found.Availability[provider]; ok {
					rows = append(rows, []string{label, humanize.CommaInt(avail.MemoryMB) + " MB free"})
				} else {
					rows = append(rows, []string{label, "no capacity sample"})
				}
			}
			return pterm.DefaultTable.WithData(rows).Render()
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "output format: json or table (default table)")
	return cmd
}

func connectionLabel(connected bool) string {
	if connected {
		return "connected"
	}
	return "disconnected"
}

func schedulingLabel(available bool) string {
	if available {
		return "available"
	}
	return "unavailable"
}

func lastSeenLabel(lastSeen *time.Time) string {
	if lastSeen == nil {
		return "no heartbeat sample"
	}
	return lastSeen.UTC().Format("2006-01-02 15:04:05 UTC")
}

func newAgentRevokeCommand(serverAddr func() string) *cobra.Command {
	var reason string
	var forceOrphanResources bool
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an agent's identity and tear down its connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rawID := args[0]
			id, err := validatePathID("agent id", rawID)
			if err != nil {
				return err
			}
			client := apiClientForServer(serverAddr())
			base := apiBaseURL(serverAddr())
			body := struct {
				Reason               string `json:"reason,omitempty"`
				ForceOrphanResources bool   `json:"force_orphan_resources,omitempty"`
			}{Reason: reason, ForceOrphanResources: forceOrphanResources}
			if err := deleteNoContentWithBody(cmd.Context(), client, base+"/api/v1/agents/"+id, body); err != nil {
				return err
			}
			msg := fmt.Sprintf("revoked agent %s", rawID)
			if forceOrphanResources {
				msg += " (resources force-orphaned)"
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "optional reason recorded with the revocation")
	cmd.Flags().BoolVar(&forceOrphanResources, "force-orphan-resources", false, "force-orphan resources still attributed to this agent (never contacts the agent; use only when it is permanently gone)")
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
	printCurlIfEnabled(req.Context(), client, req)
	resp, err := client.Do(req) //nolint:gosec // CLI requests intentionally target the user-configured Boxy server.
	if err != nil {
		return wrapConnError(err, req.URL.Host)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(resp, req.URL.String())
	}
	return nil
}
