package cli

import (
	"errors"
	"fmt"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
	"github.com/spf13/cobra"
)

// sandboxGetOutput is the JSON response for `sandbox get`.
// It embeds the sandbox and includes full Resource objects (with Properties)
// so callers can access allocation-time connection info after the fact.
type sandboxGetOutput struct {
	ID        model.SandboxID       `json:"id"`
	Name      string                `json:"name"`
	Policies  model.SandboxPolicies `json:"policies"`
	Resources []model.Resource      `json:"resources"`
}

func newSandboxGetCommand(configPath, statePath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a sandbox by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := resolveSandboxStore(*configPath, *statePath, "")
			if err != nil {
				return err
			}
			sb, err := st.GetSandbox(cmd.Context(), model.SandboxID(args[0]))
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("sandbox %q not found", args[0])
				}
				return err
			}

			resources := make([]model.Resource, 0, len(sb.Resources))
			for _, rid := range sb.Resources {
				res, err := st.GetResource(cmd.Context(), rid)
				if err != nil {
					continue // skip resources that can't be found
				}
				resources = append(resources, res)
			}

			return printJSON(sandboxGetOutput{
				ID:        sb.ID,
				Name:      sb.Name,
				Policies:  sb.Policies,
				Resources: resources,
			})
		},
	}
}
