package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newDebugProviderListCommand(opts *debugProviderOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			storePath := filepath.Join(opts.dataDir, "devfactory.json")
			data, err := os.ReadFile(storePath)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("[]")
					return nil
				}
				return fmt.Errorf("reading store: %w", err)
			}

			var store struct {
				Resources map[string]json.RawMessage `json:"resources"`
			}
			if err := json.Unmarshal(data, &store); err != nil {
				return fmt.Errorf("parsing store: %w", err)
			}

			resources := make([]json.RawMessage, 0, len(store.Resources))
			for _, r := range store.Resources {
				resources = append(resources, r)
			}
			return printJSON(resources)
		},
	}
}
