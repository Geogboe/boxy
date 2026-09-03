package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

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

type agentLogPullRequest struct {
	Since string `json:"since,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type agentLogPullResponse struct {
	RequestID string `json:"request_id"`
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
	cmd.AddCommand(newDiagnosticsExportCommand(func() string { return server }))
	cmd.AddCommand(newDiagnosticsCollectCommand(func() string { return server }))
	return cmd
}

func newDiagnosticsCollectCommand(serverAddr func() string) *cobra.Command {
	var since string
	var limit int
	cmd := &cobra.Command{
		Use:   "collect <agent-id>",
		Short: "Request retained logs from a connected agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiagnosticsCollect(cmd.Context(), args[0], since, limit, serverAddr(), cmd)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "only request events after an RFC3339 timestamp")
	cmd.Flags().IntVar(&limit, "limit", diagnostics.HardMaxLimit, "maximum number of events (1-1000)")
	return cmd
}

func runDiagnosticsCollect(ctx context.Context, rawAgent, rawSince string, limit int, server string, cmd *cobra.Command) error {
	agentID, err := validatePathID("agent id", rawAgent)
	if err != nil {
		return err
	}
	if limit < 1 || limit > diagnostics.HardMaxLimit {
		return fmt.Errorf("--limit must be between 1 and %d", diagnostics.HardMaxLimit)
	}
	since := strings.TrimSpace(rawSince)
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			return fmt.Errorf("--since must be an RFC3339 timestamp: %w", err)
		}
	}
	client, err := apiClientForCommand(cmd, server)
	if err != nil {
		return err
	}
	response, err := postJSON[agentLogPullRequest, agentLogPullResponse](ctx, client, apiBaseURL(server)+"/api/v1/agents/"+agentID+"/logs", agentLogPullRequest{Since: since, Limit: limit})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "requested logs from agent %s (request ID: %s)\n", rawAgent, response.RequestID)
	return nil
}

type diagnosticsExportOpts struct {
	since     string
	level     string
	component string
	pool      string
	agent     string
	resource  string
	limit     int
	output    string
}

func newDiagnosticsExportCommand(serverAddr func() string) *cobra.Command {
	var opts diagnosticsExportOpts
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export sanitized diagnostic events for troubleshooting",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiagnosticsExport(cmd.Context(), opts, serverAddr(), cmd)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.since, "since", "", "only events at or after an RFC3339 timestamp")
	flags.StringVar(&opts.level, "level", "", "filter level: debug, info, warn, or error")
	flags.StringVar(&opts.component, "component", "", "filter component")
	flags.StringVar(&opts.pool, "pool", "", "filter pool")
	flags.StringVar(&opts.agent, "agent", "", "filter agent")
	flags.StringVar(&opts.resource, "resource", "", "filter resource")
	flags.IntVar(&opts.limit, "limit", 1000, "maximum number of events (1-1000)")
	flags.StringVarP(&opts.output, "output", "o", "-", "output file, or - for stdout")
	return cmd
}

func runDiagnosticsExport(ctx context.Context, opts diagnosticsExportOpts, server string, cmd *cobra.Command) error {
	if opts.limit < 1 || opts.limit > diagnostics.HardMaxLimit {
		return fmt.Errorf("--limit must be between 1 and %d", diagnostics.HardMaxLimit)
	}
	client, err := apiClientForCommand(cmd, server)
	if err != nil {
		return err
	}
	values := url.Values{}
	for key, value := range map[string]string{
		"since": opts.since, "level": opts.level, "component": opts.component,
		"pool": opts.pool, "agent": opts.agent, "resource": opts.resource,
	} {
		if strings.TrimSpace(value) != "" {
			values.Set(key, value)
		}
	}
	values.Set("limit", strconv.Itoa(opts.limit))
	endpoint := apiBaseURL(server) + "/api/v1/diagnostics/export?" + values.Encode()
	archive, err := fetchJSON[diagnostics.Export](ctx, client, endpoint)
	if err != nil {
		return err
	}

	output := cmd.OutOrStdout()
	var file *os.File
	if strings.TrimSpace(opts.output) != "" && opts.output != "-" {
		file, err = os.OpenFile(opts.output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("open diagnostics export %q: %w", opts.output, err)
		}
		defer func() { _ = file.Close() }()
		output = file
	}
	return diagnostics.WriteExport(output, archive)
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
