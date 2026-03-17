package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

func newDebugProviderCreateCommand(opts *debugProviderOpts) *cobra.Command {
	var labels []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new resource",
		RunE: func(cmd *cobra.Command, args []string) error {
			d := newDebugProviderDriver(opts)

			// Parse labels (currently informational — the driver ignores them).
			_ = parseProviderLabels(labels)

			res, err := d.Create(cmd.Context(), nil)
			if err != nil {
				return err
			}
			return printJSON(map[string]any{
				"id":              res.ID,
				"connection_info": res.ConnectionInfo,
				"metadata":        res.Metadata,
			})
		},
	}

	cmd.Flags().StringArrayVar(&labels, "label", nil, "label in key=value format (repeatable)")
	return cmd
}

func parseProviderLabels(raw []string) map[string]string {
	out := make(map[string]string, len(raw))
	for _, l := range raw {
		k, v, ok := strings.Cut(l, "=")
		if ok {
			out[k] = v
		}
	}
	return out
}
