package cli

import (
	"os"

	"github.com/Geogboe/boxy/pkg/providersdk/providers/devfactory"
	"github.com/spf13/cobra"
)

const defaultProviderDataDir = ".devfactory"

type debugProviderOpts struct {
	dataDir string
	profile string
}

func newDebugProviderCommand() *cobra.Command {
	var opts debugProviderOpts

	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Exercise the devfactory reference provider",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&opts.dataDir, "data-dir", envOrDefault("DEVFACTORY_DATA_DIR", defaultProviderDataDir), "data directory for provider state")
	cmd.PersistentFlags().StringVar(&opts.profile, "profile", envOrDefault("DEVFACTORY_PROFILE", "container"), "resource profile (container, vm, share)")

	cmd.AddCommand(newDebugProviderCreateCommand(&opts))
	cmd.AddCommand(newDebugProviderListCommand(&opts))
	cmd.AddCommand(newDebugProviderGetCommand(&opts))
	cmd.AddCommand(newDebugProviderExecCommand(&opts))
	cmd.AddCommand(newDebugProviderSetStateCommand(&opts))
	cmd.AddCommand(newDebugProviderDeleteCommand(&opts))

	return cmd
}

func newDebugProviderDriver(opts *debugProviderOpts) *devfactory.Driver {
	return devfactory.New(&devfactory.Config{
		DataDir: opts.dataDir,
		Profile: devfactory.Profile(opts.profile),
	})
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
