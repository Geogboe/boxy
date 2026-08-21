package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newPoolCommand() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage pools",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&server, "server", "", "server URL (default 127.0.0.1:9090)")
	cmd.PersistentFlags().String("ca-cert", "", "Boxy CA certificate for a self-signed server")
	cmd.PersistentFlags().Bool("insecure", false, "skip HTTPS certificate verification (development only)")

	var value string
	setCredential := &cobra.Command{
		Use:   "set-guest-credential <pool>",
		Short: "Set a pool's guest bootstrap credential from stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if value != "-" {
				return fmt.Errorf("--value must be - so the credential is read from stdin")
			}
			name, err := validatePathID("pool name", args[0])
			if err != nil {
				return err
			}
			data, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("read guest credential from stdin: %w", err)
			}
			credential := strings.TrimSpace(string(data))
			if credential == "" {
				return fmt.Errorf("guest credential read from stdin must not be blank")
			}
			client, err := apiClientForCommand(cmd, server)
			if err != nil {
				return err
			}
			_, err = postJSON[map[string]string, map[string]any](cmd.Context(), client, apiBaseURL(server)+"/api/v1/pools/"+name+"/guest-credential", map[string]string{"value": credential})
			if err != nil {
				return fmt.Errorf("set guest credential for pool %q: %w", args[0], err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "guest bootstrap credential configured for pool %s\n", args[0])
			return nil
		},
	}
	setCredential.Flags().StringVar(&value, "value", "", "must be -; read the credential from stdin")
	cmd.AddCommand(setCredential)
	return cmd
}
