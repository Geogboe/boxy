package cli

import (
	"errors"
	"fmt"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/spf13/cobra"
)

// sandboxGetOutput is the JSON response for `sandbox get`.
// It embeds the sandbox and includes full Resource objects (with Properties)
// so callers can access allocation-time connection info after the fact.
type sandboxGetOutput struct {
	ID        model.SandboxID         `json:"id"`
	Name      string                  `json:"name"`
	Policies  model.SandboxPolicies   `json:"policies"`
	Status    model.SandboxStatus     `json:"status"`
	Requests  []model.ResourceRequest `json:"requests,omitempty"`
	Error     string                  `json:"error,omitempty"`
	Resources []model.Resource        `json:"resources"`
}

func newSandboxGetCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a sandbox by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := defaultAPIClient()
			base := apiBaseURL(serverAddr())

			sb, err := fetchJSON[model.Sandbox](cmd.Context(), client, base+"/api/v1/sandboxes/"+args[0])
			if err != nil {
				var apiErr *apiError
				if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
					return fmt.Errorf("sandbox %q not found", args[0])
				}
				return fmt.Errorf("get sandbox %q: %w", args[0], err)
			}

			resources := make([]model.Resource, 0, len(sb.Resources))
			for _, rid := range sb.Resources {
				res, err := fetchJSON[model.Resource](cmd.Context(), client, base+"/api/v1/resources/"+string(rid))
				if err != nil {
					var apiErr *apiError
					if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
						continue
					}
					continue // skip resources that can't be found
				}
				resources = append(resources, res)
			}

			return printJSON(sandboxGetOutput{
				ID:        sb.ID,
				Name:      sb.Name,
				Policies:  sb.Policies,
				Status:    sb.Status,
				Requests:  sb.Requests,
				Error:     sb.Error,
				Resources: resources,
			})
		},
	}
}
