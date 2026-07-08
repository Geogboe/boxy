//go:build !devtools

package cli

import "github.com/spf13/cobra"

// registerDevtoolsDebugCommands is a no-op in release builds. Development-only
// debug commands (e.g. `debug provider`, which drives the devfactory
// reference driver directly) are only available when built with
// `-tags devtools`. See #68.
func registerDevtoolsDebugCommands(cmd *cobra.Command) {}
