package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Geogboe/boxy/pkg/diagnostics"
	"github.com/spf13/cobra"
)

type diagnosticsLogsOpts struct {
	since     string
	level     string
	component string
	pool      string
	agent     string
	resource  string
	limit     int
	cursor    string
	format    string
}

type diagnosticsPageResponse struct {
	Events     []diagnostics.Event `json:"events"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func newDiagnosticsCommand() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "diagnostics",
		Short: "Inspect safe administrator diagnostics",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&server, "server", "", "server address (overrides BOXY_SERVER and the global client default)")
	cmd.PersistentFlags().String("ca-cert", "", "Boxy CA certificate for a self-signed server")
	cmd.PersistentFlags().Bool("insecure", false, "skip HTTPS certificate verification (development only)")
	cmd.AddCommand(newDiagnosticsLogsCommand(func() string { return server }))
	return cmd
}

func newDiagnosticsLogsCommand(serverAddr func() string) *cobra.Command {
	var opts diagnosticsLogsOpts
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Query bounded redacted diagnostic events",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiagnosticsLogs(cmd.Context(), opts, serverAddr(), cmd)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.since, "since", "", "only events at or after an RFC3339 timestamp")
	flags.StringVar(&opts.level, "level", "", "filter level: debug, info, warn, or error")
	flags.StringVar(&opts.component, "component", "", "filter component")
	flags.StringVar(&opts.pool, "pool", "", "filter pool")
	flags.StringVar(&opts.agent, "agent", "", "filter agent")
	flags.StringVar(&opts.resource, "resource", "", "filter resource")
	flags.IntVar(&opts.limit, "limit", 100, "maximum number of events (1-1000)")
	flags.StringVar(&opts.cursor, "cursor", "", "opaque pagination cursor")
	flags.StringVar(&opts.format, "format", "table", "output format: table or json")
	return cmd
}

func runDiagnosticsLogs(ctx context.Context, opts diagnosticsLogsOpts, server string, cmd *cobra.Command) error {
	if opts.format != "table" && opts.format != "json" {
		return fmt.Errorf("unknown --format %q: want table or json", opts.format)
	}
	if opts.limit < 1 || opts.limit > 1000 {
		return fmt.Errorf("--limit must be between 1 and 1000")
	}
	client, err := apiClientForCommand(cmd, server)
	if err != nil {
		return err
	}
	values := url.Values{}
	for key, value := range map[string]string{
		"since": opts.since, "level": opts.level, "component": opts.component,
		"pool": opts.pool, "agent": opts.agent, "resource": opts.resource, "cursor": opts.cursor,
	} {
		if strings.TrimSpace(value) != "" {
			values.Set(key, value)
		}
	}
	values.Set("limit", strconv.Itoa(opts.limit))
	endpoint := apiBaseURL(server) + "/api/v1/diagnostics/logs?" + values.Encode()
	page, err := fetchJSON[diagnosticsPageResponse](ctx, client, endpoint)
	if err != nil {
		return err
	}
	if opts.format == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(page)
	}
	for _, event := range page.Events {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", event.Timestamp.Format("2006-01-02T15:04:05Z07:00"), event.Level, event.Component, event.Message)
	}
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "next cursor: %s\n", page.NextCursor)
	}
	return nil
}
