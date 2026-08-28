package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// newBootstrapPasswordCommand reads the daemon's own one-time local-admin
// bootstrap password file directly off disk. Deliberately local, not
// REST-backed like the rest of `boxy admin` (see newAdminCommand's --server
// flag) or the API-key loopback-bootstrap endpoint it parallels: it must
// work to log into the web UI in the first place, before any session
// exists, so it reads the same .boxy/ directory `boxy serve` itself
// resolves (see serveBootstrapPasswordPath) rather than calling the API.
func newBootstrapPasswordCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "bootstrap-password",
		Short: "Show and clear the one-time local admin web-UI bootstrap password",
		Long: "Reads the local admin account's one-time bootstrap password, written by\n" +
			"`boxy serve` the first time it starts against a given state directory.\n" +
			"The password file is deleted after a successful read: if you lose it,\n" +
			"log in with it once, then set a new password from the web UI profile\n" +
			"page (not yet implemented) or re-bootstrap by deleting the local admin\n" +
			"account from state and restarting the daemon.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfgPath, err := loadConfig(configPath)
			if err != nil {
				return err
			}
			passwordPath, err := serveBootstrapPasswordPath(cfgPath)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(passwordPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no bootstrap password is pending at %q — it may have already been retrieved, or `boxy serve` has not started against this state directory yet", passwordPath)
				}
				return fmt.Errorf("read bootstrap password file %q: %w", passwordPath, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "username: admin\n")
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "password: %s\n", strings.TrimSpace(string(raw)))
			if err := os.Remove(passwordPath); err != nil {
				return fmt.Errorf("this password will not be shown again; failed to delete %q after showing it: %w", passwordPath, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "This password will not be shown again.")
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "config file path (.yaml/.yml/.json); default: ./boxy.yaml or ./boxy.yml if present — must match the config `boxy serve` was started with")
	return cmd
}
