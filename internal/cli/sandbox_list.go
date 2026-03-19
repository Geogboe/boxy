package cli

import (
	"fmt"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

func newSandboxListCommand(configPath, statePath, file *string) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := resolveSandboxStore(*configPath, *statePath, *file)
			if err != nil {
				return err
			}
			sbs, err := st.ListSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			if format == "json" {
				return printJSON(sbs)
			}
			if len(sbs) == 0 {
				pterm.Println("  No sandboxes found.")
				return nil
			}
			rows := [][]string{{"ID", "NAME", "RESOURCES"}}
			for _, sb := range sbs {
				rows = append(rows, []string{
					string(sb.ID),
					sb.Name,
					fmt.Sprintf("%d", len(sb.Resources)),
				})
			}
			return pterm.DefaultTable.WithHasHeader().WithData(rows).Render()
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "output format: json")
	return cmd
}
