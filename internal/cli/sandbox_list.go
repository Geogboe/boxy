package cli

import (
	"fmt"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

func newSandboxListCommand(serverAddr func() string) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())

			sbs, err := fetchJSON[[]model.Sandbox](cmd.Context(), client, base+"/api/v1/sandboxes")
			if err != nil {
				return fmt.Errorf("list sandboxes: %w", err)
			}
			if format == "json" {
				return printJSON(sbs)
			}
			if len(sbs) == 0 {
				pterm.Println("  No sandboxes found.")
				return nil
			}
			rows := [][]string{{"ID", "NAME", "STATUS", "RESOURCES"}}
			for _, sb := range sbs {
				rows = append(rows, []string{
					string(sb.ID),
					sb.Name,
					string(sb.Status),
					fmt.Sprintf("%d", len(sb.Resources)),
				})
			}
			return pterm.DefaultTable.WithHasHeader().WithData(rows).Render()
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "output format: json")
	return cmd
}
