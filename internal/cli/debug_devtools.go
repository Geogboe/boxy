//go:build devtools

package cli

import "github.com/spf13/cobra"

// registerDevtoolsDebugCommands wires development-only debug commands into
// the `boxy debug` tree. Only compiled into `devtools`-tagged builds.
func registerDevtoolsDebugCommands(cmd *cobra.Command) {
	cmd.AddCommand(newDebugProviderCommand())
}
